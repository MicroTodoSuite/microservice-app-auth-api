package main

// Operational contract for auth-api (spec 009, T072).
//
// These cover the behaviour the platform depends on but the business tests do
// not touch: how Kubernetes decides this pod is alive, how a request is traced
// across services, and how the service behaves when users-api is slow or down.
//
// The resilience tests matter most. auth-api calls users-api on every login,
// and the default Go HTTP client has no timeout at all — a users-api that
// accepts connections and never answers would hang auth-api's goroutines until
// the pod exhausts memory, while its liveness probe kept passing because the
// process is technically running. That is the failure this file exists to
// prevent.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

// --- health endpoints ------------------------------------------------------

// Kubernetes needs three distinct answers, and collapsing them is a real
// outage: if readiness and liveness are the same endpoint, a pod that is
// temporarily unable to serve gets killed instead of removed from the Service.
func TestHealthEndpointsAreDistinct(t *testing.T) {
	e := echo.New()
	registerOperationalRoutes(e, newHealthState())

	for _, tc := range []struct {
		name string
		path string
	}{
		{"startup", "/health/startup"},
		{"readiness", "/health/ready"},
		{"liveness", "/health/live"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("%s probe returned %d, want 200", tc.path, rec.Code)
			}

			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("%s probe body is not JSON: %v", tc.path, err)
			}
			if body["status"] != "ok" {
				t.Fatalf("%s probe reported %v, want ok", tc.path, body["status"])
			}
		})
	}
}

// Readiness must be able to fail while liveness still passes. A service that
// reports itself ready during shutdown keeps receiving traffic it cannot
// serve; one whose liveness fails at the same moment gets restarted mid-drain.
func TestReadinessFailsIndependentlyOfLiveness(t *testing.T) {
	state := newHealthState()
	e := echo.New()
	registerOperationalRoutes(e, state)

	state.setReady(false)

	readyRec := httptest.NewRecorder()
	e.ServeHTTP(readyRec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if readyRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness returned %d while not ready, want 503", readyRec.Code)
	}

	liveRec := httptest.NewRecorder()
	e.ServeHTTP(liveRec, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if liveRec.Code != http.StatusOK {
		t.Fatalf("liveness returned %d while merely not ready, want 200: a pod draining connections must not be restarted", liveRec.Code)
	}
}

// --- correlation -----------------------------------------------------------

// One request crossing four services is only debuggable if the same id appears
// in every log line it produced. The id must be echoed back so a caller can
// quote it in a bug report.
func TestCorrelationIDIsAcceptedAndEchoed(t *testing.T) {
	e := echo.New()
	e.Use(correlationMiddleware())
	e.GET("/version", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/version", nil)
	req.Header.Set(correlationHeader, "caller-supplied-id")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if got := rec.Header().Get(correlationHeader); got != "caller-supplied-id" {
		t.Fatalf("response correlation id = %q, want the caller's id echoed back", got)
	}
}

// A caller that supplies nothing must still be traceable, or the first hop in
// every untraced request becomes a dead end.
func TestCorrelationIDIsGeneratedWhenAbsent(t *testing.T) {
	e := echo.New()
	e.Use(correlationMiddleware())
	e.GET("/version", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/version", nil))

	if got := rec.Header().Get(correlationHeader); got == "" {
		t.Fatal("no correlation id was generated for a request that arrived without one")
	}
}

// The id has to reach the outbound call, or the trail stops at auth-api and
// users-api logs an unattributable request.
func TestCorrelationIDPropagatesToUsersAPI(t *testing.T) {
	var forwarded string

	service := UserService{
		Client: httpDoerFunc(func(req *http.Request) (*http.Response, error) {
			forwarded = req.Header.Get(correlationHeader)
			return jsonResponse(http.StatusOK, `{"username":"admin","firstname":"Foo","lastname":"Bar","role":"admin"}`), nil
		}),
		UserAPIAddress:    "http://users-api",
		AllowedUserHashes: map[string]interface{}{"admin_admin": nil},
	}

	ctx := withCorrelationID(context.Background(), "downstream-id")
	if _, err := service.Login(ctx, "admin", "admin"); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	if forwarded != "downstream-id" {
		t.Fatalf("outbound correlation header = %q, want downstream-id", forwarded)
	}
}

// --- resilience ------------------------------------------------------------

// The specific hang this guards: a users-api that accepts the connection and
// never responds. Without a client timeout the goroutine waits forever.
func TestUsersAPICallTimesOut(t *testing.T) {
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-blocked:
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	t.Cleanup(upstream.Close)

	service := UserService{
		Client:            newResilientClient(resilienceConfig{Timeout: 100 * time.Millisecond, MaxRetries: 0}),
		UserAPIAddress:    upstream.URL,
		AllowedUserHashes: map[string]interface{}{"admin_admin": nil},
	}

	done := make(chan error, 1)
	go func() {
		_, err := service.Login(context.Background(), "admin", "admin")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a users-api that never responds must produce an error, not a success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the users-api call did not time out: auth-api would hang a goroutine per login until the pod runs out of memory")
	}
}

// A retry must be bounded and must not apply to a real answer. Retrying a 401
// would turn one rejected login into several and look like a brute-force
// attempt to anything watching users-api.
func TestTransientFailureIsRetriedButAuthorizationFailureIsNot(t *testing.T) {
	t.Run("transient error is retried", func(t *testing.T) {
		var attempts int
		// The fake is the transport the retry wrapper calls, not a replacement
		// for it: assigning to client.Transport would discard the retries this
		// test exists to verify.
		client := newResilientClientWithBase(
			resilienceConfig{Timeout: time.Second, MaxRetries: 2},
			roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				attempts++
				if attempts < 3 {
					return nil, errors.New("connection refused")
				}
				return jsonResponse(http.StatusOK, `{"username":"admin"}`), nil
			}),
		)

		service := UserService{Client: client, UserAPIAddress: "http://users-api", AllowedUserHashes: map[string]interface{}{"admin_admin": nil}}
		if _, err := service.Login(context.Background(), "admin", "admin"); err != nil {
			t.Fatalf("a call that succeeds on the third attempt must succeed: %v", err)
		}
		if attempts != 3 {
			t.Fatalf("attempts = %d, want exactly 3 (initial + 2 retries)", attempts)
		}
	})

	t.Run("401 is not retried", func(t *testing.T) {
		var attempts int
		client := newResilientClientWithBase(
			resilienceConfig{Timeout: time.Second, MaxRetries: 2},
			roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				attempts++
				return jsonResponse(http.StatusUnauthorized, `{"error":"nope"}`), nil
			}),
		)

		service := UserService{Client: client, UserAPIAddress: "http://users-api", AllowedUserHashes: map[string]interface{}{"admin_admin": nil}}
		if _, err := service.Login(context.Background(), "admin", "admin"); err == nil {
			t.Fatal("a 401 from users-api must surface as an error")
		}
		if attempts != 1 {
			t.Fatalf("attempts = %d, want 1: retrying a rejected credential multiplies one failed login into several", attempts)
		}
	})
}

// Once users-api is clearly down, continuing to call it wastes the caller's
// time and keeps load on a service that is trying to recover. The breaker must
// open, and it must recover on its own rather than needing a restart.
func TestCircuitBreakerOpensAndRecovers(t *testing.T) {
	breaker := newCircuitBreaker(circuitBreakerConfig{
		FailureThreshold: 3,
		OpenDuration:     50 * time.Millisecond,
	})

	failing := func() error { return errors.New("users-api is down") }

	for i := 0; i < 3; i++ {
		if err := breaker.Do(failing); err == nil {
			t.Fatalf("call %d should have failed", i+1)
		}
	}

	if err := breaker.Do(func() error {
		t.Fatal("the breaker is open; the underlying call must not be attempted")
		return nil
	}); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("an open breaker must return ErrCircuitOpen, got %v", err)
	}

	time.Sleep(80 * time.Millisecond)

	if err := breaker.Do(func() error { return nil }); err != nil {
		t.Fatalf("the breaker must probe again after its open window and close on success, got %v", err)
	}
}

// A breaker shared across request goroutines that races is worse than none: it
// can report a state nobody set.
func TestCircuitBreakerIsSafeUnderConcurrency(t *testing.T) {
	breaker := newCircuitBreaker(circuitBreakerConfig{FailureThreshold: 5, OpenDuration: time.Second})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = breaker.Do(func() error {
				if n%2 == 0 {
					return errors.New("boom")
				}
				return nil
			})
		}(i)
	}
	wg.Wait()
}

// --- configuration and feature toggles -------------------------------------

// Every toggle defaults off. A toggle that defaults on ships its behaviour to
// production the moment it merges, which defeats the point of having one.
func TestFeatureTogglesDefaultOff(t *testing.T) {
	t.Setenv("AUTH_API_FEATURE_VERBOSE_ERRORS", "")

	cfg := loadRuntimeConfig()
	if cfg.Features.VerboseErrors {
		t.Fatal("verbose errors must default off; an enabled-by-default toggle reaches production on merge")
	}
}

func TestFeatureTogglesAreExplicitlyEnabled(t *testing.T) {
	t.Setenv("AUTH_API_FEATURE_VERBOSE_ERRORS", "true")

	cfg := loadRuntimeConfig()
	if !cfg.Features.VerboseErrors {
		t.Fatal("an explicitly enabled toggle must be honoured")
	}
}

// Runtime config comes from the environment with usable defaults, so a missing
// ConfigMap key degrades to a sane value instead of a zero timeout, which would
// mean "fail instantly, always".
func TestRuntimeConfigDefaultsAreUsable(t *testing.T) {
	t.Setenv("AUTH_API_USERS_TIMEOUT_MS", "")
	t.Setenv("AUTH_API_USERS_MAX_RETRIES", "")

	cfg := loadRuntimeConfig()

	if cfg.Resilience.Timeout <= 0 {
		t.Fatalf("timeout default = %v, want a positive duration: zero means every call fails instantly", cfg.Resilience.Timeout)
	}
	if cfg.Resilience.MaxRetries < 0 {
		t.Fatalf("retry default = %d, want >= 0", cfg.Resilience.MaxRetries)
	}
}

// A non-secret value must never be read from a secret-shaped variable, and the
// config struct must not carry the JWT secret at all: config is logged during
// startup debugging, and secrets that live in loggable structs end up in logs.
func TestRuntimeConfigCarriesNoSecretMaterial(t *testing.T) {
	t.Setenv("JWT_SECRET", "super-secret-value")

	cfg := loadRuntimeConfig()
	rendered, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("config must be serialisable for startup logging: %v", err)
	}
	if strings.Contains(string(rendered), "super-secret-value") {
		t.Fatal("the runtime config carries the JWT secret; config structs get logged and this would leak it")
	}
}

// --- metrics ---------------------------------------------------------------

// The golden-signal dashboards and the error-rate alert both read these names.
// Renaming one silently empties a panel and disarms an alert.
func TestMetricsExposeGoldenSignalSeries(t *testing.T) {
	e := echo.New()
	e.Use(metricsMiddleware())
	registerOperationalRoutes(e, newHealthState())
	e.GET("/version", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })

	// A Prometheus vec exports nothing until a label combination is observed,
	// so a request has to happen before the scrape. That also makes this test
	// prove the middleware records under these names, not merely that someone
	// declared them.
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/version", nil))

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("/metrics returned %d, want 200", rec.Code)
	}

	body := rec.Body.String()
	for _, series := range []string{
		"auth_api_requests_total",
		"auth_api_request_duration_seconds",
	} {
		if !strings.Contains(body, series) {
			t.Errorf("/metrics is missing %s, which the golden-signal dashboard and error-rate alert both query", series)
		}
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
