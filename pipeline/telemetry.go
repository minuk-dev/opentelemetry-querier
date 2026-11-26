package pipeline

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// Telemetry records bounded metrics and traces for query execution.
type Telemetry struct {
	tracer       trace.Tracer
	requests     metric.Int64Counter
	inFlight     metric.Int64UpDownCounter
	duration     metric.Float64Histogram
	cacheLookups metric.Int64Counter
}

type telemetryKey struct{}

// NewTelemetry binds query instruments to the supplied providers.
func NewTelemetry(meters metric.MeterProvider, tracers trace.TracerProvider) (*Telemetry, error) {
	meter := meters.Meter("opentelemetry-querier/query")
	requests, requestErr := meter.Int64Counter("querier.query.requests")
	inflight, inflightErr := meter.Int64UpDownCounter("querier.query.in_flight")
	duration, durationErr := meter.Float64Histogram("querier.query.stage.duration", metric.WithUnit("s"))
	cache, cacheErr := meter.Int64Counter("querier.query.cache.lookups")
	if err := errors.Join(requestErr, inflightErr, durationErr, cacheErr); err != nil {
		return nil, err
	}
	return &Telemetry{tracer: tracers.Tracer("opentelemetry-querier/query"), requests: requests, inFlight: inflight, duration: duration, cacheLookups: cache}, nil
}

func (t *Telemetry) request(ctx context.Context, q *qdata.Query) (context.Context, func(error)) {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		header := make(http.Header)
		for _, key := range []string{"Traceparent", "Tracestate"} {
			values := qdata.HeaderValuesFor(q, key)
			if len(values) == 1 {
				header.Set(key, values[0])
			}
		}
		ctx = propagation.TraceContext{}.Extract(ctx, propagation.HeaderCarrier(header))
	}
	_, nested := ctx.Value(telemetryKey{}).(*Telemetry)
	ctx = context.WithValue(ctx, telemetryKey{}, t)
	ctx = processor.WithCacheObserver(ctx, t.cache)
	ctx, done := t.stage(ctx, "request")
	if nested {
		return ctx, done
	}
	t.inFlight.Add(ctx, 1)
	return ctx, func(err error) {
		t.requests.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", queryOutcome(err))))
		t.inFlight.Add(ctx, -1)
		done(err)
	}
}

func (t *Telemetry) cache(ctx context.Context, hit bool) {
	outcome := "miss"
	if hit {
		outcome = "hit"
	}
	t.cacheLookups.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

func (t *Telemetry) stage(ctx context.Context, name string) (context.Context, func(error)) {
	if t == nil {
		return ctx, func(error) {}
	}
	started := time.Now()
	ctx, span := t.tracer.Start(ctx, "query."+name)
	return ctx, func(err error) {
		outcome := attribute.String("outcome", queryOutcome(err))
		t.duration.Record(ctx, time.Since(started).Seconds(), metric.WithAttributes(attribute.String("stage", name), outcome))
		span.SetAttributes(outcome)
		span.End()
	}
}

func queryOutcome(err error) string {
	if err == nil {
		return "success"
	}
	switch qerror.CodeOf(err) {
	case qerror.CodeInvalidArgument:
		return "invalid"
	case qerror.CodeUnauthenticated:
		return "unauthenticated"
	case qerror.CodePermissionDenied:
		return "denied"
	case qerror.CodeResourceExhausted:
		return "throttled"
	case qerror.CodeDeadlineExceeded:
		return "deadline"
	case qerror.CodeCanceled:
		return "canceled"
	case qerror.CodeUnavailable:
		return "backend_unavailable"
	case qerror.CodeQueryExecution:
		return "backend_execution_error"
	default:
		return "error"
	}
}
