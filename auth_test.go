package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDoer is a stub HTTPDoer that returns a canned response (or error), so the
// Users-API boundary is exercised without a real server (research D3: auth-api
// stubs users-api at the HTTP boundary).
type fakeDoer struct {
	resp *http.Response
	err  error
	last *http.Request
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.last = req
	return f.resp, f.err
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func newUserService(doer HTTPDoer) UserService {
	return UserService{
		Client:         doer,
		UserAPIAddress: "http://users-api.test",
		AllowedUserHashes: map[string]interface{}{
			"admin_admin": nil,
			"johnd_foo":   nil,
		},
	}
}

func TestGetUserAPIToken_SignsWithSecretAndClaims(t *testing.T) {
	svc := newUserService(&fakeDoer{})

	tokenStr, err := svc.getUserAPIToken("johnd")
	require.NoError(t, err)
	require.NotEmpty(t, tokenStr)

	parsed, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(jwtSecret), nil
	})
	require.NoError(t, err)
	require.True(t, parsed.Valid)

	claims, ok := parsed.Claims.(jwt.MapClaims)
	require.True(t, ok)
	assert.Equal(t, "johnd", claims["username"])
	assert.Equal(t, "read", claims["scope"])
}

func TestLogin_ValidCredentialsReturnsUser(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusOK,
		`{"username":"admin","firstname":"Admin","lastname":"User","role":"admin"}`)}
	svc := newUserService(doer)

	user, err := svc.Login(context.Background(), "admin", "admin")

	require.NoError(t, err)
	assert.Equal(t, "admin", user.Username)
	assert.Equal(t, "admin", user.Role)
	// The outbound request must carry a bearer token to the Users API.
	require.NotNil(t, doer.last)
	assert.Equal(t, "http://users-api.test/users/admin", doer.last.URL.String())
	assert.True(t, strings.HasPrefix(doer.last.Header.Get("Authorization"), "Bearer "))
}

func TestLogin_WrongPasswordReturnsWrongCredentials(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusOK, `{"username":"admin"}`)}
	svc := newUserService(doer)

	_, err := svc.Login(context.Background(), "admin", "not-the-password")

	assert.Equal(t, ErrWrongCredentials, err)
}

func TestLogin_UsersAPIErrorPropagates(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusInternalServerError, "boom")}
	svc := newUserService(doer)

	_, err := svc.Login(context.Background(), "admin", "admin")

	require.Error(t, err)
	assert.NotEqual(t, ErrWrongCredentials, err)
}

func TestVersionHandler(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := func(c echo.Context) error {
		return c.String(http.StatusOK, "Auth API, written in Go\n")
	}

	require.NoError(t, handler(c))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Auth API, written in Go")
}

func TestLoginHandler_ValidReturnsSignedToken(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusOK,
		`{"username":"admin","firstname":"Admin","lastname":"User","role":"admin"}`)}
	svc := newUserService(doer)

	e := echo.New()
	body := `{"username":"admin","password":"admin"}`
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	require.NoError(t, getLoginHandler(svc)(c))
	assert.Equal(t, http.StatusOK, rec.Code)

	var out map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Contains(t, out, "accessToken")

	parsed, err := jwt.Parse(out["accessToken"], func(*jwt.Token) (interface{}, error) {
		return []byte(jwtSecret), nil
	})
	require.NoError(t, err)
	claims := parsed.Claims.(jwt.MapClaims)
	assert.Equal(t, "admin", claims["username"])
	assert.Equal(t, "admin", claims["role"])
}

func TestLoginHandler_WrongCredentialsReturns401(t *testing.T) {
	doer := &fakeDoer{resp: jsonResponse(http.StatusOK, `{"username":"admin"}`)}
	svc := newUserService(doer)

	e := echo.New()
	body := `{"username":"admin","password":"wrong"}`
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := getLoginHandler(svc)(c)
	require.Error(t, err)
	httpErr, ok := err.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusUnauthorized, httpErr.Code)
}

func TestLoginHandler_MalformedBodyReturnsGenericError(t *testing.T) {
	svc := newUserService(&fakeDoer{resp: jsonResponse(http.StatusOK, `{}`)})

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader([]byte("not-json")))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := getLoginHandler(svc)(c)
	require.Error(t, err)
	httpErr, ok := err.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusInternalServerError, httpErr.Code)
}
