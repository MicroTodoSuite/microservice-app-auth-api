package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
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
