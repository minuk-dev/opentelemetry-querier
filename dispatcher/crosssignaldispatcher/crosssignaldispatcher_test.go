package crosssignaldispatcher_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher/crosssignaldispatcher"
	qdatav1 "github.com/minuk-dev/opentelemetry-querier/gen/qdata/v1"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// fakeDispatcher returns a preset result (or error), recording the query it saw.
type fakeDispatcher struct {
	dispatcher.Base

	result *qdata.Result
	err    error
	seen   *qdata.Query
}

func (f *fakeDispatcher) Dispatch(_ context.Context, query *qdata.Query) (*qdata.Result, error) {
	f.seen = query

	return f.result, f.err
}

// metricResult is a one-series metrics result labelled job=api, name up.
func metricResult(value float64) *qdata.Result {
	attrs := &qdata.KeyValueList{}
	qdata.AttrPutString(attrs, "job", "api")

	series := &qdata.MetricSeries{
		Name:       "up",
		Attributes: attrs,
		Points:     []*qdata.MetricPoint{{Value: qdata.Double(value)}},
	}

	return &qdata.Result{
		Signal: qdata.SignalMetrics,
		Data:   &qdatav1.Result_Metrics{Metrics: &qdata.Metrics{Series: []*qdata.MetricSeries{series}}},
	}
}

// logResult is a one-record logs result labelled by job, body "boom".
func logResult(job string) *qdata.Result {
	attrs := &qdata.KeyValueList{}
	qdata.AttrPutString(attrs, "job", job)

	record := &qdata.LogRecord{Attributes: attrs, Body: qdata.Str("boom")}

	return &qdata.Result{
		Signal: qdata.SignalLogs,
		Data:   &qdatav1.Result_Logs{Logs: &qdata.Logs{Records: []*qdata.LogRecord{record}}},
	}
}

// joinPlan builds a metrics/logs cross-signal plan joined on the given labels.
func joinPlan(onLabels ...string) *qdata.QueryPlan {
	lhs := qdata.SelectNode(qdata.SignalMetrics, qdata.LeafPredicate(
		&qdata.LabelMatcher{Name: "__name__", Op: qdata.MatchEqual, Value: "up"}))
	rhs := qdata.SelectNode(qdata.SignalLogs, qdata.LeafPredicate(
		&qdata.LabelMatcher{Name: "job", Op: qdata.MatchEqual, Value: "api"}))

	return qdata.Plan(qdata.BinaryNode(qdata.BinDiv, lhs, rhs, &qdata.VectorMatch{On: onLabels}))
}

// rowValue returns the string form of column in the first row of the result's
// table.
func rowValue(t *testing.T, result *qdata.Result, column string) string {
	t.Helper()

	require.NotEmpty(t, result.GetTable().GetRows(), "table has rows")

	value, ok := qdata.AttrGet(result.GetTable().GetRows()[0].GetValues(), column)
	require.True(t, ok, "row has column %q", column)

	return value.GetStringValue()
}

func TestSingleSignalRoutes(t *testing.T) {
	t.Parallel()

	backend := &fakeDispatcher{Base: dispatcher.Base{}, result: metricResult(1), err: nil, seen: nil}
	sut := crosssignaldispatcher.New(map[qdata.Signal]dispatcher.Dispatcher{qdata.SignalMetrics: backend})

	query := &qdata.Query{Plan: qdata.Plan(qdata.SelectNode(qdata.SignalMetrics, nil))}

	result, err := sut.Dispatch(context.Background(), query)
	require.NoError(t, err)
	assert.Same(t, backend.result, result, "single-signal query is delegated whole")
	assert.Same(t, query, backend.seen, "the original query is passed through unchanged")
}

func TestJoinMetricsAndLogs(t *testing.T) {
	t.Parallel()

	metrics := &fakeDispatcher{Base: dispatcher.Base{}, result: metricResult(0.5), err: nil, seen: nil}
	logs := &fakeDispatcher{Base: dispatcher.Base{}, result: logResult("api"), err: nil, seen: nil}
	sut := crosssignaldispatcher.New(map[qdata.Signal]dispatcher.Dispatcher{
		qdata.SignalMetrics: metrics,
		qdata.SignalLogs:    logs,
	})

	result, err := sut.Dispatch(context.Background(), &qdata.Query{Plan: joinPlan("job")})
	require.NoError(t, err)

	require.NotNil(t, result.GetTable(), "cross-signal result is a Table")
	assert.Equal(t, qdata.SignalUnspecified, result.GetSignal())
	require.NoError(t, qdata.ValidateTable(result.GetTable()))
	require.Len(t, result.GetTable().GetRows(), 1, "one metrics row joins one logs row on job")

	assert.Equal(t, "api", rowValue(t, result, "job"))
	assert.Equal(t, "up", rowValue(t, result, "__name__"))
	assert.Equal(t, "boom", rowValue(t, result, "body"))
	assert.InDelta(t, 0.5, mustDouble(t, result, "value"), 1e-9)
}

func TestJoinOnSharedColumnsWhenNoOnGiven(t *testing.T) {
	t.Parallel()

	metrics := &fakeDispatcher{Base: dispatcher.Base{}, result: metricResult(0.5), err: nil, seen: nil}
	logs := &fakeDispatcher{Base: dispatcher.Base{}, result: logResult("api"), err: nil, seen: nil}
	sut := crosssignaldispatcher.New(map[qdata.Signal]dispatcher.Dispatcher{
		qdata.SignalMetrics: metrics,
		qdata.SignalLogs:    logs,
	})

	// No `on`: the join falls back to the shared "job" column.
	result, err := sut.Dispatch(context.Background(), &qdata.Query{Plan: joinPlan()})
	require.NoError(t, err)
	require.Len(t, result.GetTable().GetRows(), 1)
	assert.Equal(t, "api", rowValue(t, result, "job"))
}

func TestNonMatchingKeysProduceNoRows(t *testing.T) {
	t.Parallel()

	metrics := &fakeDispatcher{Base: dispatcher.Base{}, result: metricResult(0.5), err: nil, seen: nil}
	logs := &fakeDispatcher{Base: dispatcher.Base{}, result: logResult("worker"), err: nil, seen: nil}
	sut := crosssignaldispatcher.New(map[qdata.Signal]dispatcher.Dispatcher{
		qdata.SignalMetrics: metrics,
		qdata.SignalLogs:    logs,
	})

	result, err := sut.Dispatch(context.Background(), &qdata.Query{Plan: joinPlan("job")})
	require.NoError(t, err)
	assert.Empty(t, result.GetTable().GetRows(), "job=api never matches job=worker")
}

func TestFailClosed(t *testing.T) {
	t.Parallel()

	metrics := &fakeDispatcher{Base: dispatcher.Base{}, result: metricResult(1), err: nil, seen: nil}
	logs := &fakeDispatcher{Base: dispatcher.Base{}, result: logResult("api"), err: nil, seen: nil}
	full := map[qdata.Signal]dispatcher.Dispatcher{qdata.SignalMetrics: metrics, qdata.SignalLogs: logs}

	t.Run("no plan", func(t *testing.T) {
		t.Parallel()

		_, err := crosssignaldispatcher.New(full).Dispatch(context.Background(), &qdata.Query{})
		require.Error(t, err)
	})

	t.Run("missing backend for single signal", func(t *testing.T) {
		t.Parallel()

		sut := crosssignaldispatcher.New(map[qdata.Signal]dispatcher.Dispatcher{})
		_, err := sut.Dispatch(context.Background(),
			&qdata.Query{Plan: qdata.Plan(qdata.SelectNode(qdata.SignalMetrics, nil))})
		require.Error(t, err)
	})

	t.Run("multi-signal plan that is not a top-level binary", func(t *testing.T) {
		t.Parallel()

		// A function whose args span two signals: multi-signal but not a join.
		plan := qdata.Plan(qdata.FunctionNode("vector", []*qdata.Node{
			qdata.SelectNode(qdata.SignalMetrics, nil),
			qdata.SelectNode(qdata.SignalLogs, nil),
		}))
		_, err := crosssignaldispatcher.New(full).Dispatch(context.Background(), &qdata.Query{Plan: plan})
		require.Error(t, err)
	})

	t.Run("no join key", func(t *testing.T) {
		t.Parallel()

		// Metrics labelled by region, logs by zone: no shared column and no `on`.
		regionMetrics := &fakeDispatcher{Base: dispatcher.Base{}, result: metricByLabel("region", "eu"), err: nil, seen: nil}
		zoneLogs := &fakeDispatcher{Base: dispatcher.Base{}, result: logByLabel("zone", "a"), err: nil, seen: nil}
		sut := crosssignaldispatcher.New(map[qdata.Signal]dispatcher.Dispatcher{
			qdata.SignalMetrics: regionMetrics,
			qdata.SignalLogs:    zoneLogs,
		})
		_, err := sut.Dispatch(context.Background(), &qdata.Query{Plan: joinPlan()})
		require.Error(t, err)
	})
}

func metricByLabel(key, value string) *qdata.Result {
	attrs := &qdata.KeyValueList{}
	qdata.AttrPutString(attrs, key, value)

	series := &qdata.MetricSeries{Attributes: attrs, Points: []*qdata.MetricPoint{{Value: qdata.Double(1)}}}

	return &qdata.Result{
		Signal: qdata.SignalMetrics,
		Data:   &qdatav1.Result_Metrics{Metrics: &qdata.Metrics{Series: []*qdata.MetricSeries{series}}},
	}
}

func logByLabel(key, value string) *qdata.Result {
	attrs := &qdata.KeyValueList{}
	qdata.AttrPutString(attrs, key, value)

	record := &qdata.LogRecord{Attributes: attrs, Body: qdata.Str("x")}

	return &qdata.Result{
		Signal: qdata.SignalLogs,
		Data:   &qdatav1.Result_Logs{Logs: &qdata.Logs{Records: []*qdata.LogRecord{record}}},
	}
}

func mustDouble(t *testing.T, result *qdata.Result, column string) float64 {
	t.Helper()

	value, ok := qdata.AttrGet(result.GetTable().GetRows()[0].GetValues(), column)
	require.True(t, ok, "row has column %q", column)

	return value.GetDoubleValue()
}
