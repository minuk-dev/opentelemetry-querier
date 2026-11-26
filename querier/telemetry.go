package querier

import (
	"context"
	"errors"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func newTelemetry(ctx context.Context) (*pipeline.Telemetry, func(context.Context) error, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	spans, err := otlptracegrpc.New(ctx)
	if err != nil {
		return nil, nil, err
	}
	metrics, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		_ = spans.Shutdown(ctx)
		return nil, nil, err
	}
	tracing := sdktrace.NewTracerProvider(sdktrace.WithBatcher(spans))
	meters := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metrics)))
	stop := func(ctx context.Context) error {
		return errors.Join(tracing.Shutdown(ctx), meters.Shutdown(ctx))
	}
	telemetry, err := pipeline.NewTelemetry(meters, tracing)
	if err != nil {
		_ = stop(ctx)
		return nil, nil, err
	}
	return telemetry, stop, nil
}
