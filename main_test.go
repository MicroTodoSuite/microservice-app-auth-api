package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
)

func TestLoginHandlerReturnsSignedUserClaims(t *testing.T) {
	originalSecret := jwtSecret
	jwtSecret = "unit-test-secret"
	t.Cleanup(func() { jwtSecret = originalSecret })

	service := UserService{
		Client: httpDoerFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"username":"admin","firstname":"Foo","lastname":"Bar","role":"admin"}`), nil
		}),
		UserAPIAddress:    "http://users-api",
		AllowedUserHashes: map[string]interface{}{"admin_admin": nil},
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"username":"admin","password":"admin"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	recorder := httptest.NewRecorder()

	if err := getLoginHandler(service)(e.NewContext(req, recorder)); err != nil {
		t.Fatalf("login handler returned an error: %v", err)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d", recorder.Code, http.StatusOK)
	}

	var payload map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	token, err := jwt.Parse(payload["accessToken"], func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodHS256 {
			t.Fatalf("unexpected signing method: %s", token.Method.Alg())
		}
		return []byte(jwtSecret), nil
	})
	if err != nil || !token.Valid {
		t.Fatalf("access token is not valid: %v", err)
	}
	claims := token.Claims.(jwt.MapClaims)
	if claims["username"] != "admin" || claims["role"] != "admin" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
}

func TestLoginHandlerRejectsWrongCredentials(t *testing.T) {
	service := UserService{
		Client: httpDoerFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, `{"username":"admin"}`), nil
		}),
		UserAPIAddress:    "http://users-api",
		AllowedUserHashes: map[string]interface{}{},
	}
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"username":"admin","password":"wrong"}`))
	recorder := httptest.NewRecorder()

	err := getLoginHandler(service)(e.NewContext(req, recorder))
	if err != ErrWrongCredentials {
		t.Fatalf("login error = %v, want %v", err, ErrWrongCredentials)
	}
}

func TestLoginHandlerRejectsMalformedJSON(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("{"))
	recorder := httptest.NewRecorder()

	err := getLoginHandler(UserService{})(e.NewContext(req, recorder))
	if err != ErrHttpGenericMessage {
		t.Fatalf("login error = %v, want %v", err, ErrHttpGenericMessage)
	}
}
