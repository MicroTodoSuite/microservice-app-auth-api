package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	jwt "github.com/dgrijalva/jwt-go"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	gommonlog "github.com/labstack/gommon/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// Prometheus counter for tracking the number of requests handled by the Auth API
	requestCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auth_api_requests_total",
			Help: "Total number of requests handled by the Auth API",
		},
		[]string{"method", "status"},
	)
)

var (
	// ErrHttpGenericMessage is returned for generic errors, details should be logged
	ErrHttpGenericMessage = echo.NewHTTPError(http.StatusInternalServerError, "something went wrong, please try again later")

	// ErrWrongCredentials indicates that login attempt failed because of incorrect login or password
	ErrWrongCredentials = echo.NewHTTPError(http.StatusUnauthorized, "username or password is invalid")

	// Default JWT secret key
	jwtSecret = "myfancysecret"
)

func main() {
	// Register Prometheus metrics
	prometheus.MustRegister(requestCount)

	// Retrieve configuration from environment variables
	hostport := ":" + os.Getenv("AUTH_API_PORT")
	userAPIAddress := os.Getenv("USERS_API_ADDRESS")

	// Override default JWT secret if specified in environment variables
	envJwtSecret := os.Getenv("JWT_SECRET")
	if len(envJwtSecret) != 0 {
		jwtSecret = envJwtSecret
	}

	// Initialize UserService with allowed user hashes
	userService := UserService{
		Client:         http.DefaultClient,
		UserAPIAddress: userAPIAddress,
		AllowedUserHashes: map[string]interface{}{
			"admin_admin": nil,
			"johnd_foo":   nil,
			"janed_ddd":   nil,
		},
	}

	// Create a new Echo instance
	e := echo.New()

	// Middleware to count requests
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			method := c.Request().Method
			status := http.StatusOK
			defer func() {
				requestCount.WithLabelValues(method, fmt.Sprintf("%d", status)).Inc()
			}()
			err := next(c)
			if err != nil {
				if httpError, ok := err.(*echo.HTTPError); ok {
					status = httpError.Code
				}
			}
			return err
		}
	})

	// Route for Prometheus metrics
	e.GET("/metrics", echo.WrapHandler(promhttp.Handler()))

	// Set log level
	e.Logger.SetLevel(gommonlog.INFO)

	if zipkinURL := os.Getenv("ZIPKIN_URL"); len(zipkinURL) != 0 {
		e.Logger.Infof("init tracing to Zipkit at %s", zipkinURL)

		if tracedMiddleware, tracedClient, err := initTracing(zipkinURL); err == nil {
			e.Use(echo.WrapMiddleware(tracedMiddleware))
			userService.Client = tracedClient
		} else {
			e.Logger.Infof("Zipkin tracer init failed: %s", err.Error())
		}
	} else {
		e.Logger.Infof("Zipkin URL was not provided, tracing is not initialised")
	}

	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())

	// Route => handler
	e.GET("/version", func(c echo.Context) error {
		return c.String(http.StatusOK, "Auth API, written in Go\n")
	})

	e.POST("/login", getLoginHandler(userService))

	// Start server
	e.Logger.Fatal(e.Start(hostport))
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func getLoginHandler(userService UserService) echo.HandlerFunc {
	f := func(c echo.Context) error {
		requestData := LoginRequest{}
		decoder := json.NewDecoder(c.Request().Body)
		if err := decoder.Decode(&requestData); err != nil {
			log.Printf("could not read credentials from POST body: %s", err.Error())
			return ErrHttpGenericMessage
		}

		ctx := c.Request().Context()
		user, err := userService.Login(ctx, requestData.Username, requestData.Password)
		if err != nil {
			if err != ErrWrongCredentials {
				log.Printf("could not authorize user '%s': %s", requestData.Username, err.Error())
				return ErrHttpGenericMessage
			}

			return ErrWrongCredentials
		}
		token := jwt.New(jwt.SigningMethodHS256)

		// Set claims
		claims := token.Claims.(jwt.MapClaims)
		claims["username"] = user.Username
		claims["firstname"] = user.FirstName
		claims["lastname"] = user.LastName
		claims["role"] = user.Role
		claims["exp"] = time.Now().Add(time.Hour * 72).Unix()

		// Generate encoded token and send it as response.
		t, err := token.SignedString([]byte(jwtSecret))
		if err != nil {
			log.Printf("could not generate a JWT token: %s", err.Error())
			return ErrHttpGenericMessage
		}

		return c.JSON(http.StatusOK, map[string]string{
			"accessToken": t,
		})
	}

	return echo.HandlerFunc(f)
}
