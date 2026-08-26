package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	gommonlog "github.com/labstack/gommon/log"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"
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

	// Prometheus histogram for the golden-signal latency dashboard
	// (infrastructure/prometheus/rules/golden-signals.yaml in gitops).
	requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "auth_api_request_duration_seconds",
			Help:    "Duration of requests handled by the Auth API",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method"},
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
	// Retrieve configuration from environment variables
	hostport := ":" + os.Getenv("AUTH_API_PORT")
	userAPIAddress := os.Getenv("USERS_API_ADDRESS")

	// Override default JWT secret if specified in environment variables
	envJwtSecret := os.Getenv("JWT_SECRET")
	if len(envJwtSecret) != 0 {
		jwtSecret = envJwtSecret
	}

	// Non-secret operational configuration, supplied by ConfigMap. Loaded
	// before anything else so a bad value fails startup rather than surfacing
	// as an odd timeout hours later.
	cfg := loadRuntimeConfig()

	// http.DefaultClient has no timeout. A users-api that accepts the
	// connection and never answers would hang one goroutine per login until
	// this pod exhausts memory, while liveness kept passing.
	userService := UserService{
		Client:         newResilientClient(cfg.Resilience),
		UserAPIAddress: userAPIAddress,
		AllowedUserHashes: map[string]interface{}{
			"admin_admin": nil,
			"johnd_foo":   nil,
			"janed_ddd":   nil,
		},
	}

	// Create a new Echo instance
	e := echo.New()

	e.Use(metricsMiddleware())

	// Health probes and Prometheus metrics. Registered before the auth
	// middleware chain: a probe that needs a credential fails during exactly
	// the incident it exists to report on.
	health := newHealthState()
	registerOperationalRoutes(e, health)

	// Set log level
	e.Logger.SetLevel(gommonlog.INFO)

	if otlpEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); len(otlpEndpoint) != 0 {
		e.Logger.Infof("init OpenTelemetry tracing to %s", otlpEndpoint)

		if shutdown, tracedClient, err := initTracing(context.Background()); err == nil {
			e.Use(otelecho.Middleware("auth-api"))
			userService.Client = tracedClient
			defer shutdown(context.Background())
		} else {
			e.Logger.Infof("OpenTelemetry tracer init failed: %s", err.Error())
		}
	} else {
		e.Logger.Infof("OTEL_EXPORTER_OTLP_ENDPOINT was not provided, tracing is not initialised")
	}

	// Ahead of the request logger, so every line it writes carries the id.
	e.Use(correlationMiddleware())
	e.Use(requestLoggerMiddleware())
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())

	// Route => handler
	e.GET("/version", func(c echo.Context) error {
		return c.String(http.StatusOK, "Auth API, written in Go\n")
	})

	e.POST("/login", getLoginHandler(userService))

	e.Logger.Infof("runtime configuration: %s", marshalConfigForStartupLog(cfg))

	// Start server
	e.Logger.Fatal(e.Start(hostport))
}

// requestLoggerMiddleware replaces echo's default access-log middleware with
// a structured JSON one carrying trace_id/span_id (see otel.go), so a log
// line can be correlated back to its trace.
func requestLoggerMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			err := next(c)
			logWithTrace(c.Request().Context(), slog.LevelInfo, "http_request",
				"method", c.Request().Method,
				"path", c.Path(),
				"status", c.Response().Status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
			return err
		}
	}
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func getLoginHandler(userService UserService) echo.HandlerFunc {
	f := func(c echo.Context) error {
		ctx := c.Request().Context()
		requestData := LoginRequest{}
		decoder := json.NewDecoder(c.Request().Body)
		if err := decoder.Decode(&requestData); err != nil {
			logWithTrace(ctx, slog.LevelError, "could not read credentials from POST body", "error", err.Error())
			return ErrHttpGenericMessage
		}

		user, err := userService.Login(ctx, requestData.Username, requestData.Password)
		if err != nil {
			if err != ErrWrongCredentials {
				logWithTrace(ctx, slog.LevelError, "could not authorize user", "username", requestData.Username, "error", err.Error())
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
			logWithTrace(ctx, slog.LevelError, "could not generate a JWT token", "error", err.Error())
			return ErrHttpGenericMessage
		}

		return c.JSON(http.StatusOK, map[string]string{
			"accessToken": t,
		})
	}

	return echo.HandlerFunc(f)
}
