package pipeline_test

import (
	"context"
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestExportedQueryTelemetry(t *testing.T) {
	t.Parallel()

	recorder := tracetest.NewSpanRecorder()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	reader := sdkmetric.NewManualReader()
	meters := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	telemetry, err := pipeline.NewTelemetry(meters, traceProvider)
	require.NoError(t, err)

	backend := &stubDispatcher{Base: dispatcher.Base{}, result: &qdata.Result{}}
	pipe := pipeline.New("test", nil, backend)
	pipe.Telemetry = telemetry
	_, err = pipe.Handle(t.Context(), &qdata.Query{})
	require.NoError(t, err)

	denied := pipe.ObserveIngress(func(ctx context.Context, _ *qdata.Query) (*qdata.Result, error) {
		_, finish := pipe.StartAuthentication(ctx)
		err := qerror.New(qerror.CodePermissionDenied, "secret")
		finish(err)

		return nil, err
	})
	_, err = denied(t.Context(), &qdata.Query{})
	require.Error(t, err)

	var data metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &data))

	found := false

	for _, scope := range data.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			if instrument.Name != "querier.query.requests" {
				continue
			}

			found = true
			counts, ok := instrument.Data.(metricdata.Sum[int64])
			require.True(t, ok)
			require.Len(t, counts.DataPoints, 2)
		}
	}

	require.True(t, found)

	dispatched := 0

	for _, span := range recorder.Ended() {
		if span.Name() == "query.backend" {
			dispatched++
		}

		for _, attr := range span.Attributes() {
			require.NotContains(t, attr.Value.String(), "secret")
		}
	}

	require.Equal(t, 1, dispatched)
}
