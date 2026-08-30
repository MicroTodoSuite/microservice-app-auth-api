package main

// Operational contract for auth-api (spec 009, T077): health probes,
// correlation, runtime configuration, and the resilience wrapper around the
// users-api dependency.
//
// Kept separate from main.go because these concerns belong to the platform
// rather than to authentication: how Kubernetes decides the pod is alive, how a
// request stays traceable across four services, and how the service behaves
// when its one dependency is slow.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// correlationHeader is the id a caller may supply and that this service
// forwards to users-api. It is the standard header the Istio ingress and the
// other four services already use, so a single request produces one id across
// every log line it touches.
const correlationHeader = "X-Request-Id"

type correlationKey struct{}

// --- health ----------------------------------------------------------------

// healthState separates the three answers Kubernetes needs.
//
// Collapsing readiness into liveness is a real outage: a pod that is
// temporarily unable to serve — draining, or waiting on a dependency — would be
// restarted instead of simply removed from the Service's endpoints, turning a
// brief degradation into a crash loop.
type healthState struct {
	mu      sync.RWMutex
	started bool
	ready   bool
}

func newHealthState() *healthState {
	return &healthState{started: true, ready: true}
}

func (h *healthState) setReady(ready bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ready = ready
}

func (h *healthState) setStarted(started bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.started = started
}

func (h *healthState) snapshot() (started bool, ready bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.started, h.ready
}

// registerOperationalRoutes mounts the endpoints the platform scrapes and
// probes. They are deliberately outside any authentication middleware: a
// probe that needs a credential fails during exactly the incident it exists to
// report on.
// registerMetrics is idempotent. The collectors are registered here rather
// than in main() so /metrics answers with the real series in any process that
// mounts these routes — a test binary included. Registering twice panics, which
// is why this is guarded rather than simply called from both places.
var registerMetrics = sync.OnceFunc(func() {
	prometheus.MustRegister(requestCount)
	prometheus.MustRegister(requestDuration)
})

func registerOperationalRoutes(e *echo.Echo, state *healthState) {
	registerMetrics()

	e.GET("/health/startup", func(c echo.Context) error {
		started, _ := state.snapshot()
		if !started {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"status": "starting"})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// Readiness answers "should this pod receive traffic right now".
	e.GET("/health/ready", func(c echo.Context) error {
		_, ready := state.snapshot()
		if !ready {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"status": "not-ready"})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	// Liveness answers only "is this process wedged". It must not consult the
	// users-api dependency: a users-api outage would otherwise restart every
	// auth-api pod, removing the one component still able to serve cached work
	// and turning a dependency incident into a full outage.
	e.GET("/health/live", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	})

	e.GET("/metrics", echo.WrapHandler(promhttp.Handler()))
}

// metricsMiddleware records the golden signals under the exact series names the
// dashboards and the error-rate alert query. It lives here rather than inline in
// main() so a test can drive a request through it: a Prometheus vec exports
// nothing at all until a label combination has been observed, so "is the metric
// registered" can only be answered by producing one.
func metricsMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			method := c.Request().Method
			status := http.StatusOK

			defer func() {
				requestCount.WithLabelValues(method, strconv.Itoa(status)).Inc()
				requestDuration.WithLabelValues(method).Observe(time.Since(start).Seconds())
			}()

			err := next(c)
			if err != nil {
				if httpError, ok := err.(*echo.HTTPError); ok {
					status = httpError.Code
				}
			}
			return err
		}
	}
}

// --- correlation -----------------------------------------------------------

func withCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationKey{}, id)
}

func correlationIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(correlationKey{}).(string); ok {
		return id
	}
	return ""
}

// correlationMiddleware adopts the caller's id or mints one, echoes it back so
// a user can quote it in a bug report, and puts it on the request context so
// the outbound users-api call carries the same value.
func correlationMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			id := c.Request().Header.Get(correlationHeader)
			if id == "" {
				id = uuid.NewString()
			}

			c.Response().Header().Set(correlationHeader, id)
			c.SetRequest(c.Request().WithContext(withCorrelationID(c.Request().Context(), id)))
			return next(c)
		}
	}
}

// --- runtime configuration -------------------------------------------------

type featureToggles struct {
	// VerboseErrors returns upstream error detail to the caller. Off by
	// default and intended for a temporary, deliberate debugging window: the
	// detail it exposes is useful to an operator and equally useful to someone
	// probing the login endpoint.
	VerboseErrors bool `json:"verboseErrors"`
}

type resilienceConfig struct {
	Timeout    time.Duration `json:"timeoutMs"`
	MaxRetries int           `json:"maxRetries"`
}

type circuitBreakerConfig struct {
	FailureThreshold int           `json:"failureThreshold"`
	OpenDuration     time.Duration `json:"openDurationMs"`
}

// runtimeConfig holds non-secret operational values only.
//
// It carries no secret material by construction. Config structs get logged
// during startup debugging, and a JWT secret in a loggable struct is a secret
// in the log aggregator; the secret stays in the package-level jwtSecret that
// nothing serialises.
type runtimeConfig struct {
	Features       featureToggles       `json:"features"`
	Resilience     resilienceConfig     `json:"resilience"`
	CircuitBreaker circuitBreakerConfig `json:"circuitBreaker"`
}

func loadRuntimeConfig() runtimeConfig {
	return runtimeConfig{
		Features: featureToggles{
			// Every toggle defaults off. A toggle that defaults on ships its
			// behaviour to production the moment it merges, which is precisely
			// what having a toggle is supposed to avoid.
			VerboseErrors: envBool("AUTH_API_FEATURE_VERBOSE_ERRORS", false),
		},
		Resilience: resilienceConfig{
			// Defaults are usable rather than zero: a zero timeout means every
			// call fails instantly, so a missing ConfigMap key would take the
			// service down rather than degrade it.
			Timeout:    time.Duration(envInt("AUTH_API_USERS_TIMEOUT_MS", 2000)) * time.Millisecond,
			MaxRetries: envInt("AUTH_API_USERS_MAX_RETRIES", 2),
		},
		CircuitBreaker: circuitBreakerConfig{
			FailureThreshold: envInt("AUTH_API_USERS_BREAKER_THRESHOLD", 5),
			OpenDuration:     time.Duration(envInt("AUTH_API_USERS_BREAKER_OPEN_MS", 5000)) * time.Millisecond,
		},
	}
}

func envBool(name string, fallback bool) bool {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return value
}

func envInt(name string, fallback int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

// --- resilient HTTP client -------------------------------------------------

// retryTransport bounds how long a users-api call may take and how often it may
// be repeated.
//
// The timeout is the important half. Go's default HTTP client has no timeout at
// all, so a users-api that accepts the connection and never answers would hang
// one auth-api goroutine per login until the pod exhausts memory — while its
// liveness probe kept passing, because the process is technically running.
type retryTransport struct {
	base       http.RoundTripper
	maxRetries int
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}

	var lastErr error
	for attempt := 0; attempt <= t.maxRetries; attempt++ {
		resp, err := base.RoundTrip(req)
		if err == nil {
			// A response is an answer, even a rejecting one. Retrying a 401
			// would turn one rejected login into several and look like a
			// brute-force attempt to anything watching users-api; retrying a
			// 404 would just repeat it.
			return resp, nil
		}

		lastErr = err
		if attempt == t.maxRetries {
			break
		}

		// Back off so a dependency that is restarting is not hammered by every
		// in-flight login at once.
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(time.Duration(attempt+1) * 50 * time.Millisecond):
		}
	}

	return nil, lastErr
}

func newResilientClient(cfg resilienceConfig) *http.Client {
	return newResilientClientWithBase(cfg, nil)
}

// newResilientClientWithBase lets a caller supply the transport the retry
// wrapper drives. Tests use it to drive the retry path without a network.
func newResilientClientWithBase(cfg resilienceConfig, base http.RoundTripper) *http.Client {
	return &http.Client{
		Timeout:   cfg.Timeout,
		Transport: &retryTransport{base: base, maxRetries: cfg.MaxRetries},
	}
}

// --- circuit breaker -------------------------------------------------------

// ErrCircuitOpen is returned instead of attempting a call the breaker believes
// will fail.
var ErrCircuitOpen = errors.New("users-api circuit is open")

// circuitBreaker stops calling a dependency that is clearly down.
//
// Once users-api is failing, continuing to call it makes the caller wait for a
// timeout it already knows is coming, and keeps load on a service trying to
// recover. The breaker closes itself after its open window rather than needing
// a restart, so recovery does not require an operator.
type circuitBreaker struct {
	mu           sync.Mutex
	cfg          circuitBreakerConfig
	failures     int
	openedAt     time.Time
	isOpen       bool
	halfOpenBusy bool
}

func newCircuitBreaker(cfg circuitBreakerConfig) *circuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.OpenDuration <= 0 {
		cfg.OpenDuration = 5 * time.Second
	}
	return &circuitBreaker{cfg: cfg}
}

func (b *circuitBreaker) Do(call func() error) error {
	if err := b.beforeCall(); err != nil {
		return err
	}

	err := call()
	b.afterCall(err)
	return err
}

func (b *circuitBreaker) beforeCall() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.isOpen {
		return nil
	}

	// The open window has not elapsed: refuse without touching the dependency.
	if time.Since(b.openedAt) < b.cfg.OpenDuration {
		return ErrCircuitOpen
	}

	// Half-open: let exactly one caller probe. Letting every waiting caller
	// through at once would re-flood a dependency at the moment it is weakest.
	if b.halfOpenBusy {
		return ErrCircuitOpen
	}
	b.halfOpenBusy = true
	return nil
}

func (b *circuitBreaker) afterCall(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if err != nil {
		b.failures++
		if b.failures >= b.cfg.FailureThreshold {
			b.isOpen = true
			b.openedAt = time.Now()
		}
		b.halfOpenBusy = false
		return
	}

	b.failures = 0
	b.isOpen = false
	b.halfOpenBusy = false
}

// marshalConfigForStartupLog renders the non-secret configuration for a single
// startup log line, so an operator can see what the pod actually loaded.
func marshalConfigForStartupLog(cfg runtimeConfig) string {
	rendered, err := json.Marshal(cfg)
	if err != nil {
		return "{}"
	}
	return string(rendered)
}
