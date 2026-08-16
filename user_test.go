package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	jwt "github.com/golang-jwt/jwt/v5"
)

type httpDoerFunc func(*http.Request) (*http.Response, error)

func (f httpDoerFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestUserServiceLoginUsesScopedBearerToken(t *testing.T) {
	originalSecret := jwtSecret
	jwtSecret = "unit-test-secret"
	t.Cleanup(func() { jwtSecret = originalSecret })

	service := UserService{
		Client: httpDoerFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != "http://users-api/users/admin" {
				t.Fatalf("request URL = %q", req.URL.String())
			}
			rawToken := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
			token, err := jwt.Parse(rawToken, func(token *jwt.Token) (interface{}, error) {
				return []byte(jwtSecret), nil
			})
			if err != nil || !token.Valid {
				t.Fatalf("users-api bearer token is invalid: %v", err)
			}
			claims := token.Claims.(jwt.MapClaims)
			if claims["username"] != "admin" || claims["scope"] != "read" {
				t.Fatalf("unexpected users-api claims: %#v", claims)
			}
			return jsonResponse(http.StatusOK, `{"username":"admin","firstname":"Foo","lastname":"Bar","role":"admin"}`), nil
		}),
		UserAPIAddress:    "http://users-api",
		AllowedUserHashes: map[string]interface{}{"admin_admin": nil},
	}

	user, err := service.Login(context.Background(), "admin", "admin")
	if err != nil {
		t.Fatalf("login returned an error: %v", err)
	}
	if user.Username != "admin" || user.Role != "admin" {
		t.Fatalf("unexpected user: %#v", user)
	}
}

func TestUserServicePropagatesUsersAPIStatus(t *testing.T) {
	service := UserService{
		Client: httpDoerFunc(func(req *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusNotFound, `{"message":"missing"}`), nil
		}),
		UserAPIAddress:    "http://users-api",
		AllowedUserHashes: map[string]interface{}{"nobody_password": nil},
	}

	if _, err := service.Login(context.Background(), "nobody", "password"); err == nil {
		t.Fatal("login unexpectedly succeeded for a missing user")
	}
}
