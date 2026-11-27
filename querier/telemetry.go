package querier

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const telemetrySetupTimeout = 5 * time.Second

func (svc *Service) configureTelemetry(enabled bool) error {
	if !enabled {
		return nil
	}

	telemetry, stop, err := newTelemetry(context.Background())
	if err != nil {
		return fmt.Errorf("query telemetry: %w", err)
	}

	svc.stopTelemetry = stop
	for _, pipe := range svc.pipelines {
		pipe.Telemetry = telemetry
	}

	return nil
}

func newTelemetry(ctx context.Context) (*pipeline.Telemetry, func(context.Context) error, error) {
	ctx, cancel := context.WithTimeout(ctx, telemetrySetupTimeout)
	defer cancel()

	spans, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}

	metrics, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		_ = spans.Shutdown(ctx)

		return nil, nil, fmt.Errorf("create OTLP metric exporter: %w", err)
	}

	tracing := sdktrace.NewTracerProvider(sdktrace.WithBatcher(spans))
	meters := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metrics)))
	stop := func(ctx context.Context) error {
		return errors.Join(tracing.Shutdown(ctx), meters.Shutdown(ctx))
	}

	telemetry, err := pipeline.NewTelemetry(meters, tracing)
	if err != nil {
		_ = stop(ctx)

		return nil, nil, fmt.Errorf("create query telemetry instruments: %w", err)
	}

	return telemetry, stop, nil
}
