package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Config holds the telemetry initialisation parameters.
// Populated from config.TelemetrySpec in main.go to avoid an import cycle.
type Config struct {
	ServiceName    string
	ServiceVersion string
	// OTLPEndpoint is the OTLP gRPC endpoint (e.g. "localhost:4317").
	// When empty, no trace exporter is created.
	OTLPEndpoint string
	// Insecure disables TLS for the OTLP gRPC connection.
	Insecure bool
}

// Init initialises the OTel SDK: propagator, Prometheus meter provider, and (if configured)
// an OTLP gRPC trace exporter. Returns a shutdown function that flushes and closes all providers.
// Must be called before any HTTP handlers are set up.
func Init(ctx context.Context, cfg *Config) (shutdown func(context.Context) error, err error) {
	// Always configure W3C Trace Context propagation.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	res, resErr := sdkresource.New(ctx,
		sdkresource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
	)
	if resErr != nil {
		return nil, fmt.Errorf("creating OTel resource: %w", resErr)
	}

	// Always create a Prometheus meter provider for local metric scraping.
	promExp, err := otelprom.New()
	if err != nil {
		return nil, fmt.Errorf("creating Prometheus exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(promExp),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	if err := InitMetrics(mp); err != nil {
		return nil, fmt.Errorf("initialising metrics: %w", err)
	}

	shutdownFuncs := []func(context.Context) error{
		mp.Shutdown,
	}

	if cfg.OTLPEndpoint != "" {
		var creds credentials.TransportCredentials
		if cfg.Insecure {
			creds = insecure.NewCredentials()
		} else {
			creds = credentials.NewTLS(nil)
		}

		traceExp, expErr := otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint),
			otlptracegrpc.WithTLSCredentials(creds),
		)
		if expErr != nil {
			return nil, fmt.Errorf("creating OTLP gRPC trace exporter: %w", expErr)
		}

		tp := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(traceExp),
			sdktrace.WithResource(res),
		)
		otel.SetTracerProvider(tp)

		shutdownFuncs = append(shutdownFuncs, tp.Shutdown)
	}

	return func(shutCtx context.Context) error {
		var firstErr error
		for _, fn := range shutdownFuncs {
			if err := fn(shutCtx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}, nil
}
