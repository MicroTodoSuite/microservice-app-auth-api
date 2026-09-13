package main

// Tracing contract for auth-api (spec 010, T013).
//
// Spans are read from an in-memory recorder through the same middleware and
// HTTP client constructors main uses, so these tests observe production wiring
// rather than a hand-built copy of it.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const (
	incomingTraceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	incomingTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
)

func recordingProvider(t *testing.T) (*sdktrace.TracerProvider, *tracetest.SpanRecorder) {
	t.Helper()
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		otel.SetTextMapPropagator(previous)
		_ = provider.Shutdown(context.Background())
	})
	return provider, recorder
}

func spanNames(spans []sdktrace.ReadOnlySpan) []string {
	names := make([]string, 0, len(spans))
	for _, span := range spans {
		names = append(names, span.Name())
	}
	return names
}

func spanOfKind(spans []sdktrace.ReadOnlySpan, kind trace.SpanKind) sdktrace.ReadOnlySpan {
	for _, span := range spans {
		if span.SpanKind() == kind {
			return span
		}
	}
	return nil
}

func TestProbesAndScrapesProduceNoSpans(t *testing.T) {
	provider, recorder := recordingProvider(t)
	e := echo.New()
	e.Use(newTracingMiddleware(provider))
	registerOperationalRoutes(e, newHealthState())

	for _, path := range []string{"/health/startup", "/health/ready", "/health/live", "/metrics"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("traceparent", incomingTraceparent)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code >= http.StatusInternalServerError {
			t.Fatalf("%s answered %d", path, rec.Code)
		}
	}

	if spans := recorder.Ended(); len(spans) != 0 {
		t.Fatalf("probes and scrapes must not be traced, got spans %v", spanNames(spans))
	}
}

// tracedLogin runs one sign-in through the tracing middleware against a fake
// users-api that records the traceparent it receives.
func tracedLogin(t *testing.T, password string, allowed map[string]interface{}) (*httptest.ResponseRecorder, []sdktrace.ReadOnlySpan, string) {
	t.Helper()
	originalSecret := jwtSecret
	jwtSecret = "unit-test-secret"
	t.Cleanup(func() { jwtSecret = originalSecret })

	provider, recorder := recordingProvider(t)

	var forwarded string
	users := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = r.Header.Get("traceparent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"username":"admin","firstname":"Foo","lastname":"Bar","role":"admin"}`)
	}))
	t.Cleanup(users.Close)

	service := UserService{
		Client:            newTracedClient(provider, newResilientClient(resilienceConfig{Timeout: 2 * time.Second, MaxRetries: 1})),
		UserAPIAddress:    users.URL,
		AllowedUserHashes: allowed,
	}

	e := echo.New()
	e.Use(newTracingMiddleware(provider))
	e.POST("/login", getLoginHandler(service))

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"username":"admin","password":"`+password+`"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("traceparent", incomingTraceparent)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	return rec, recorder.Ended(), forwarded
}

func TestLoginIsTracedAndPropagatesContextToUsersAPI(t *testing.T) {
	rec, spans, forwarded := tracedLogin(t, "admin", map[string]interface{}{"admin_admin": nil})
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d", rec.Code, http.StatusOK)
	}

	server := spanOfKind(spans, trace.SpanKindServer)
	client := spanOfKind(spans, trace.SpanKindClient)
	if server == nil || client == nil {
		t.Fatalf("want one SERVER and one CLIENT span, got %v", spanNames(spans))
	}
	if got := server.SpanContext().TraceID().String(); got != incomingTraceID {
		t.Fatalf("SERVER span trace id = %s, want the incoming %s", got, incomingTraceID)
	}
	if client.Parent().SpanID() != server.SpanContext().SpanID() {
		t.Fatalf("the users-api CLIENT span must be a child of the /login SERVER span")
	}
	if !strings.Contains(forwarded, incomingTraceID) || !strings.Contains(forwarded, client.SpanContext().SpanID().String()) {
		t.Fatalf("users-api must receive the CLIENT span's context, got traceparent %q", forwarded)
	}

	var payload map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil || payload["accessToken"] == "" {
		t.Fatalf("login response carries no access token: %v", err)
	}
	for _, span := range spans {
		for _, attribute := range span.Attributes() {
			value := attribute.Value.Emit()
			if strings.Contains(value, payload["accessToken"]) || strings.Contains(strings.ToLower(value), "bearer") ||
				strings.Contains(strings.ToLower(string(attribute.Key)), "password") {
				t.Fatalf("span %q carries a credential in attribute %s", span.Name(), attribute.Key)
			}
		}
	}
}

func TestRejectedSignInRecords401WithoutMarkingTheServerSpan(t *testing.T) {
	rec, spans, _ := tracedLogin(t, "wrong", map[string]interface{}{})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	server := spanOfKind(spans, trace.SpanKindServer)
	if server == nil {
		t.Fatalf("want a SERVER span, got %v", spanNames(spans))
	}

	recorded := false
	for _, attribute := range server.Attributes() {
		key := string(attribute.Key)
		if (key == "http.response.status_code" || key == "http.status_code") && attribute.Value.AsInt64() == http.StatusUnauthorized {
			recorded = true
		}
	}
	if !recorded {
		t.Fatalf("the SERVER span must record HTTP status 401, got attributes %v", server.Attributes())
	}
	// OpenTelemetry HTTP semantic conventions: a 4xx server response leaves the
	// span status unset.
	if server.Status().Code != codes.Unset {
		t.Fatalf("SERVER span status = %v, want Unset for a 4xx response", server.Status().Code)
	}
}

// Tracing must wrap the resilient users-api client, not replace it: a traced
// client without the timeout lets a silent users-api hang a login forever.
func TestTracedClientKeepsTheResilientTimeoutAndRetries(t *testing.T) {
	provider, recorder := recordingProvider(t)

	var attempts atomic.Int32
	var forwarded string
	flaky := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if attempts.Add(1) == 1 {
			return nil, io.ErrUnexpectedEOF
		}
		forwarded = req.Header.Get("traceparent")
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}")), Request: req}, nil
	})
	base := newResilientClientWithBase(resilienceConfig{Timeout: 3 * time.Second, MaxRetries: 2}, flaky)

	client := newTracedClient(provider, base)
	if client.Timeout != base.Timeout {
		t.Fatalf("traced client timeout = %v, want the resilient client's %v", client.Timeout, base.Timeout)
	}

	req := httptest.NewRequest(http.MethodGet, "http://users-api/users/admin", nil)
	req.RequestURI = ""
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("a retryable failure must be retried behind the tracing transport: %v", err)
	}
	_ = resp.Body.Close()
	if got := attempts.Load(); got != 2 {
		t.Fatalf("users-api attempts = %d, want 2 (one failure, one retry)", got)
	}

	spans := recorder.Ended()
	span := spanOfKind(spans, trace.SpanKindClient)
	if len(spans) != 1 || span == nil {
		t.Fatalf("want one CLIENT span for the call, got %v", spanNames(spans))
	}
	if !strings.Contains(forwarded, span.SpanContext().SpanID().String()) {
		t.Fatalf("the retried request must carry the CLIENT span's context, got traceparent %q", forwarded)
	}
}
