package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

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
// returns a shutdown func plus an HTTP client that propagates trace context
// on outbound requests (the auth-api -> users-api call in user.go).
//
// The exporter reads its target from the standard OTEL_EXPORTER_OTLP_ENDPOINT
// environment variable itself (the OpenTelemetry SDK auto-configures from it);
// main.go only uses that variable's presence as the on/off switch, not as a
// literal WithEndpoint() value, since WithEndpoint() expects a bare
// host:port, not the URL form OTEL_EXPORTER_OTLP_ENDPOINT is documented to
// carry (verified live: passing the raw env value to WithEndpoint produced a
// "parse url" warning from the gRPC dialer).
func initTracing(ctx context.Context) (func(context.Context) error, *http.Client, error) {
	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithInsecure())
	if err != nil {
		return nil, nil, err
	}

	res, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(
			semconv.ServiceName("auth-api"),
		),
	)
	if err != nil {
		return nil, nil, err
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

	tracedClient := &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}

	return tracerProvider.Shutdown, tracedClient, nil
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
