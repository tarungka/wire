// Package observability wires OpenTelemetry metrics + a Prometheus
// exporter for wire. Traces are wired but currently no exporter is
// attached; that is a later phase.
//
// The package follows a single-init pattern: call Init() once at startup
// from cmd/main.go, defer the returned shutdown func. Subsystems then
// fetch their instruments lazily via Meter()/Tracer().
package observability

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Config controls OTel/metrics initialization.
type Config struct {
	// Enabled gates the entire stack. When false, Init returns a no-op
	// shutdown func and Meter()/Tracer() return no-op instruments.
	Enabled bool

	// ServiceName is the OTel service.name resource attribute. Default
	// "wire". Override per node role (e.g. "wire-coordinator", "wire-worker").
	ServiceName string

	// ServiceVersion is the OTel service.version resource attribute.
	ServiceVersion string

	// NodeID populates the service.instance.id resource attribute so
	// metrics from different nodes are distinguishable in the backend.
	NodeID string

	// MetricsAddr is the bind address of the Prometheus scrape endpoint.
	// Default ":9090". Set to empty to disable Prometheus scrape (you'd
	// only do this if you push via OTLP — not yet wired).
	MetricsAddr string
}

// DefaultConfig returns sensible defaults for the observability stack.
func DefaultConfig() Config {
	return Config{
		Enabled:     true,
		ServiceName: "wire",
		MetricsAddr: ":9090",
	}
}

var (
	once     sync.Once
	enabled  bool
	meter    metric.Meter
	provider *sdkmetric.MeterProvider
	tracer   *trace.TracerProvider
)

// Init starts the OTel metric provider, the Prometheus scrape HTTP server,
// and registers the trace provider. Returns a shutdown func that the
// caller MUST defer; it flushes pending metrics and stops the scrape
// server.
//
// Safe to call when cfg.Enabled is false — it sets the global meter to a
// no-op provider and returns a no-op shutdown.
func Init(ctx context.Context, cfg Config, log zerolog.Logger) (shutdown func(context.Context) error, err error) {
	var initErr error
	once.Do(func() {
		if !cfg.Enabled {
			meter = otel.Meter("wire")
			shutdown = func(context.Context) error { return nil }
			return
		}
		if cfg.ServiceName == "" {
			cfg.ServiceName = "wire"
		}

		// Resource attributes attached to every metric/trace.
		attrs := []resource.Option{
			resource.WithAttributes(
				semconv.ServiceName(cfg.ServiceName),
			),
		}
		if cfg.ServiceVersion != "" {
			attrs = append(attrs, resource.WithAttributes(semconv.ServiceVersion(cfg.ServiceVersion)))
		}
		if cfg.NodeID != "" {
			attrs = append(attrs, resource.WithAttributes(semconv.ServiceInstanceID(cfg.NodeID)))
		}
		res, err := resource.New(ctx, attrs...)
		if err != nil {
			initErr = fmt.Errorf("observability: build resource: %w", err)
			return
		}

		// Prometheus exporter — wires OTel metrics to a Prometheus
		// scrape endpoint. The Reader is a sdkmetric.Reader.
		promExp, err := prometheus.New()
		if err != nil {
			initErr = fmt.Errorf("observability: prometheus exporter: %w", err)
			return
		}

		// View overrides for *.duration histograms — OTel's default buckets
		// max at 10 000 of-unit, which assumes the unit is milliseconds.
		// We declare durations in seconds, so install bucket boundaries
		// that span 100 ns → 10 s, the realistic range for wire ops.
		latencyBuckets := sdkmetric.NewView(
			sdkmetric.Instrument{Kind: sdkmetric.InstrumentKindHistogram},
			sdkmetric.Stream{
				Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
					Boundaries: []float64{
						0.0000001, 0.0000005,
						0.000001, 0.000005,
						0.00001, 0.00005,
						0.0001, 0.0005,
						0.001, 0.005,
						0.01, 0.05,
						0.1, 0.5,
						1, 5,
						10,
					},
				},
			},
		)

		// Job lifecycle durations span a much wider range than per-op
		// latency: a stub uppercase pipeline finishes in milliseconds
		// while a real streaming job runs for hours before cancellation.
		// Override the catch-all latency view by name so this histogram
		// gets buckets from 10 ms to 1 h. View resolution picks the
		// FIRST matching view, so this must be registered before the
		// kind-based latency view.
		jobDurationBuckets := sdkmetric.NewView(
			sdkmetric.Instrument{Name: "wire.coordinator.job.duration"},
			sdkmetric.Stream{
				Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
					Boundaries: []float64{
						0.01, 0.05,
						0.1, 0.5,
						1, 5,
						10, 30,
						60, 300,
						900, 1800,
						3600,
					},
				},
			},
		)

		provider = sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(promExp),
			sdkmetric.WithView(jobDurationBuckets),
			sdkmetric.WithView(latencyBuckets),
		)
		otel.SetMeterProvider(provider)
		meter = provider.Meter(
			"wire",
			metric.WithInstrumentationVersion(cfg.ServiceVersion),
			metric.WithInstrumentationAttributes(),
			metric.WithSchemaURL(""),
		)

		// Trace provider stays in the no-export configuration for now;
		// future phase will plug in an OTLP exporter behind a flag.
		tracer = trace.NewTracerProvider(
			trace.WithResource(res),
		)
		otel.SetTracerProvider(tracer)

		var srv *http.Server
		var shutdownPromHTTP func(context.Context) error
		shutdownPromHTTP = func(context.Context) error { return nil }
		if cfg.MetricsAddr != "" {
			ln, lnErr := net.Listen("tcp", cfg.MetricsAddr)
			if lnErr != nil {
				initErr = fmt.Errorf("observability: bind metrics endpoint %s: %w", cfg.MetricsAddr, lnErr)
				return
			}
			mux := http.NewServeMux()
			mux.Handle("/metrics", promhttp.Handler())
			srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
			log.Info().Str("addr", ln.Addr().String()).Msg("metrics endpoint listening")
			go func() {
				if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Error().Err(err).Msg("metrics endpoint exited")
				}
			}()
			shutdownPromHTTP = srv.Shutdown
		}

		// Suppress instrumentation lib warnings.
		_ = instrumentation.Scope{}

		enabled = true

		shutdown = func(ctx context.Context) error {
			var firstErr error
			if err := shutdownPromHTTP(ctx); err != nil {
				firstErr = err
			}
			if err := provider.Shutdown(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
			if err := tracer.Shutdown(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
			return firstErr
		}
	})
	return shutdown, initErr
}

// Meter returns the wire meter, or the global no-op meter if Init has not
// been called.
func Meter() metric.Meter {
	if meter == nil {
		return otel.Meter("wire")
	}
	return meter
}

// Enabled reports whether observability has been successfully initialised.
func Enabled() bool { return enabled }
