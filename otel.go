package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// initTracing wires the OpenTelemetry SDK to an OTLP/gRPC collector and
// returns the tracer provider main passes to newTracingMiddleware and
// newTracedClient. Shut the provider down on exit to flush batched spans.
//
// The exporter reads its target from the standard OTEL_EXPORTER_OTLP_ENDPOINT
// environment variable itself (the OpenTelemetry SDK auto-configures from it);
// main.go only uses that variable's presence as the on/off switch, not as a
// literal WithEndpoint() value, since WithEndpoint() expects a bare
// host:port, not the URL form OTEL_EXPORTER_OTLP_ENDPOINT is documented to
// carry (verified live: passing the raw env value to WithEndpoint produced a
// "parse url" warning from the gRPC dialer).
func initTracing(ctx context.Context) (*sdktrace.TracerProvider, error) {
	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithInsecure())
	if err != nil {
		return nil, err
	}

	res, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(
			semconv.ServiceName("auth-api"),
		),
	)
	if err != nil {
		return nil, err
	}

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tracerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tracerProvider, nil
}

// newTracingMiddleware traces inbound requests, continuing an incoming W3C
// traceparent. Probes and scrapes are skipped: they would bury every sign-in
// trace under kubelet and Prometheus traffic.
func newTracingMiddleware(provider trace.TracerProvider) echo.MiddlewareFunc {
	return otelecho.Middleware("auth-api",
		otelecho.WithTracerProvider(provider),
		otelecho.WithSkipper(isProbeOrScrape),
	)
}

func isProbeOrScrape(c echo.Context) bool {
	path := c.Request().URL.Path
	return path == "/metrics" || strings.HasPrefix(path, "/health/")
}

// newTracedClient wraps base's transport so each users-api call is a CLIENT
// span that propagates its context. It copies base rather than replacing it,
// so the timeout and retries of newResilientClient still apply; retries run
// inside the one CLIENT span.
func newTracedClient(provider trace.TracerProvider, base *http.Client) *http.Client {
	traced := *base
	traced.Transport = otelhttp.NewTransport(base.Transport, otelhttp.WithTracerProvider(provider))
	return &traced
}

// structuredLogger is a minimal JSON logger correlated to the active span,
// per OpenTelemetry's recommended trace_id/span_id log correlation fields.
// It replaces the plain-text log.Printf calls that carried no trace context.
var structuredLogger = slog.New(slog.NewJSONHandler(os.Stdout, nil))

// logWithTrace attaches trace_id/span_id from ctx (if a sampled span is
// present) to a structured log line at the given level.
func logWithTrace(ctx context.Context, level slog.Level, msg string, args ...any) {
	spanContext := trace.SpanContextFromContext(ctx)
	if spanContext.IsValid() {
		args = append(args, "trace_id", spanContext.TraceID().String(), "span_id", spanContext.SpanID().String())
	}
	structuredLogger.Log(ctx, level, msg, args...)
}
