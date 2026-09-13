package main

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// OpenTelemetry metrics for auth-api (gitops spec 011). The instruments keep
// the exact series the golden-signal recording rules and the canary gate read.
// The OpenTelemetry Prometheus exporter writes them into metricsRegistry, a
// registry of its own rather than client_golang's default, so /metrics serves
// no go_ or process_ runtime series (spec 011 FR-005); scope labels and
// target_info are off so every series keeps today's label set.

// client_golang v1.24.1's DefBuckets, preserved by spec 011 research R3.
var requestDurationBoundaries = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

var (
	metricsRegistry = prometheus.NewRegistry()
	requestCount    metric.Int64Counter
	requestDuration metric.Float64Histogram
)

func init() {
	exporter, err := otelprometheus.New(
		otelprometheus.WithRegisterer(metricsRegistry),
		otelprometheus.WithoutScopeInfo(),
		otelprometheus.WithoutTargetInfo(),
	)
	if err != nil {
		panic(err)
	}
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: "auth_api_request_duration_seconds"},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: requestDurationBoundaries}},
		)),
	)
	meter := provider.Meter("auth-api")

	// The exporter appends _total to a monotonic counter.
	if requestCount, err = meter.Int64Counter("auth_api_requests",
		metric.WithDescription("Total number of requests handled by the Auth API")); err != nil {
		panic(err)
	}
	if requestDuration, err = meter.Float64Histogram("auth_api_request_duration_seconds",
		metric.WithDescription("Duration of requests handled by the Auth API")); err != nil {
		panic(err)
	}
}

// metricsHandler serves the exporter's registry.
func metricsHandler() http.Handler {
	return promhttp.HandlerFor(metricsRegistry, promhttp.HandlerOpts{})
}
