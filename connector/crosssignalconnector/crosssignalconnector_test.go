package crosssignalconnector_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/connector/crosssignalconnector"
	qdatav1 "github.com/minuk-dev/opentelemetry-querier/gen/qdata/v1"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// fakeHandler returns a preset result (or error), recording the query it saw.
type fakeHandler struct {
	result *qdata.Result
	err    error
	seen   *qdata.Query
}

func (f *fakeHandler) Handle(_ context.Context, query *qdata.Query) (*qdata.Result, error) {
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

	return qdata.Plan(qdata.BinaryNode(qdata.BinAnd, lhs, rhs, &qdata.VectorMatch{On: onLabels}))
}

// rowValue returns the string form of column in the first row of the result table.
func rowValue(t *testing.T, result *qdata.Result, column string) string {
	t.Helper()

	require.NotEmpty(t, result.GetTable().GetRows(), "table has rows")

	value, ok := qdata.AttrGet(result.GetTable().GetRows()[0].GetValues(), column)
	require.True(t, ok, "row has column %q", column)

	return value.GetStringValue()
}

func TestSingleSignalRoutes(t *testing.T) {
	t.Parallel()

	target := &fakeHandler{result: metricResult(1), err: nil, seen: nil}
	sut := crosssignalconnector.New(map[qdata.Signal]pipeline.Handler{qdata.SignalMetrics: target})

	query := &qdata.Query{Plan: qdata.Plan(qdata.SelectNode(qdata.SignalMetrics, nil))}

	result, err := sut.Dispatch(context.Background(), query)
	require.NoError(t, err)
	assert.Same(t, target.result, result, "single-signal query is delegated whole")
	assert.Same(t, query, target.seen, "the original query is passed through to its pipeline")
}

func TestJoinMetricsAndLogs(t *testing.T) {
	t.Parallel()

	metrics := &fakeHandler{result: metricResult(0.5), err: nil, seen: nil}
	logs := &fakeHandler{result: logResult("api"), err: nil, seen: nil}
	sut := crosssignalconnector.New(map[qdata.Signal]pipeline.Handler{
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

	metrics := &fakeHandler{result: metricResult(0.5), err: nil, seen: nil}
	logs := &fakeHandler{result: logResult("api"), err: nil, seen: nil}
	sut := crosssignalconnector.New(map[qdata.Signal]pipeline.Handler{
		qdata.SignalMetrics: metrics,
		qdata.SignalLogs:    logs,
	})

	result, err := sut.Dispatch(context.Background(), &qdata.Query{Plan: joinPlan()})
	require.NoError(t, err)
	require.Len(t, result.GetTable().GetRows(), 1)
	assert.Equal(t, "api", rowValue(t, result, "job"))
}

func TestNonMatchingKeysProduceNoRows(t *testing.T) {
	t.Parallel()

	metrics := &fakeHandler{result: metricResult(0.5), err: nil, seen: nil}
	logs := &fakeHandler{result: logResult("worker"), err: nil, seen: nil}
	sut := crosssignalconnector.New(map[qdata.Signal]pipeline.Handler{
		qdata.SignalMetrics: metrics,
		qdata.SignalLogs:    logs,
	})

	result, err := sut.Dispatch(context.Background(), &qdata.Query{Plan: joinPlan("job")})
	require.NoError(t, err)
	assert.Empty(t, result.GetTable().GetRows(), "job=api never matches job=worker")
}

// metricResultJobs is a metrics result with one series per job label.
func metricResultJobs(jobs ...string) *qdata.Result {
	series := make([]*qdata.MetricSeries, 0, len(jobs))

	for _, job := range jobs {
		attrs := &qdata.KeyValueList{}
		qdata.AttrPutString(attrs, "job", job)
		series = append(series, &qdata.MetricSeries{
			Name:       "up",
			Attributes: attrs,
			Points:     []*qdata.MetricPoint{{Value: qdata.Double(1)}},
		})
	}

	return &qdata.Result{
		Signal: qdata.SignalMetrics,
		Data:   &qdatav1.Result_Metrics{Metrics: &qdata.Metrics{Series: series}},
	}
}

func TestUnlessAntiJoin(t *testing.T) {
	t.Parallel()

	metrics := &fakeHandler{result: metricResultJobs("api", "worker"), err: nil, seen: nil}
	logs := &fakeHandler{result: logResult("api"), err: nil, seen: nil}
	sut := crosssignalconnector.New(map[qdata.Signal]pipeline.Handler{
		qdata.SignalMetrics: metrics,
		qdata.SignalLogs:    logs,
	})

	lhs := qdata.SelectNode(qdata.SignalMetrics, nil)
	rhs := qdata.SelectNode(qdata.SignalLogs, nil)
	plan := qdata.Plan(qdata.BinaryNode(qdata.BinUnless, lhs, rhs, &qdata.VectorMatch{On: []string{"job"}}))

	result, err := sut.Dispatch(context.Background(), &qdata.Query{Plan: plan})
	require.NoError(t, err)
	require.Len(t, result.GetTable().GetRows(), 1, "UNLESS keeps only the metric job with no matching log")
	assert.Equal(t, "worker", rowValue(t, result, "job"))
}

func TestUnsupportedOperatorFailsClosed(t *testing.T) {
	t.Parallel()

	metrics := &fakeHandler{result: metricResult(0.5), err: nil, seen: nil}
	logs := &fakeHandler{result: logResult("api"), err: nil, seen: nil}
	sut := crosssignalconnector.New(map[qdata.Signal]pipeline.Handler{
		qdata.SignalMetrics: metrics,
		qdata.SignalLogs:    logs,
	})

	for _, operator := range []qdata.BinOp{qdata.BinDiv, qdata.BinOr} {
		lhs := qdata.SelectNode(qdata.SignalMetrics, nil)
		rhs := qdata.SelectNode(qdata.SignalLogs, nil)
		plan := qdata.Plan(qdata.BinaryNode(operator, lhs, rhs, &qdata.VectorMatch{On: []string{"job"}}))

		_, err := sut.Dispatch(context.Background(), &qdata.Query{Plan: plan})
		require.Error(t, err, "operator %v is not a relational join and must fail closed", operator)
	}
}

func TestIgnoringNarrowsJoinKeys(t *testing.T) {
	t.Parallel()

	metricAttrs := &qdata.KeyValueList{}
	qdata.AttrPutString(metricAttrs, "job", "api")
	qdata.AttrPutString(metricAttrs, "instance", "m1")
	metricRes := &qdata.Result{Signal: qdata.SignalMetrics, Data: &qdatav1.Result_Metrics{
		Metrics: &qdata.Metrics{Series: []*qdata.MetricSeries{{
			Name: "up", Attributes: metricAttrs, Points: []*qdata.MetricPoint{{Value: qdata.Double(0.5)}},
		}}},
	}}

	logAttrs := &qdata.KeyValueList{}
	qdata.AttrPutString(logAttrs, "job", "api")
	qdata.AttrPutString(logAttrs, "instance", "l1")
	logRes := &qdata.Result{Signal: qdata.SignalLogs, Data: &qdatav1.Result_Logs{
		Logs: &qdata.Logs{Records: []*qdata.LogRecord{{Attributes: logAttrs, Body: qdata.Str("boom")}}},
	}}

	sut := crosssignalconnector.New(map[qdata.Signal]pipeline.Handler{
		qdata.SignalMetrics: &fakeHandler{result: metricRes, err: nil, seen: nil},
		qdata.SignalLogs:    &fakeHandler{result: logRes, err: nil, seen: nil},
	})

	lhs := qdata.SelectNode(qdata.SignalMetrics, nil)
	rhs := qdata.SelectNode(qdata.SignalLogs, nil)

	plainPlan := qdata.Plan(qdata.BinaryNode(qdata.BinAnd, lhs, rhs, &qdata.VectorMatch{}))
	plain, err := sut.Dispatch(context.Background(), &qdata.Query{Plan: plainPlan})
	require.NoError(t, err)
	assert.Empty(t, plain.GetTable().GetRows(), "differing instance labels block the shared-column join")

	ignorePlan := qdata.Plan(qdata.BinaryNode(qdata.BinAnd, lhs, rhs, &qdata.VectorMatch{Ignoring: []string{"instance"}}))
	ignored, err := sut.Dispatch(context.Background(), &qdata.Query{Plan: ignorePlan})
	require.NoError(t, err)
	require.Len(t, ignored.GetTable().GetRows(), 1, "ignoring(instance) narrows the key to job and joins")
	assert.Equal(t, "api", rowValue(t, ignored, "job"))
}

func TestValueLabelIsPreservedNotClobbered(t *testing.T) {
	t.Parallel()

	attrs := &qdata.KeyValueList{}
	qdata.AttrPutString(attrs, "job", "api")
	qdata.AttrPutString(attrs, "value", "user-label")
	metricRes := &qdata.Result{Signal: qdata.SignalMetrics, Data: &qdatav1.Result_Metrics{
		Metrics: &qdata.Metrics{Series: []*qdata.MetricSeries{{
			Name: "up", Attributes: attrs, Points: []*qdata.MetricPoint{{Value: qdata.Double(0.5)}},
		}}},
	}}

	sut := crosssignalconnector.New(map[qdata.Signal]pipeline.Handler{
		qdata.SignalMetrics: &fakeHandler{result: metricRes, err: nil, seen: nil},
		qdata.SignalLogs:    &fakeHandler{result: logResult("api"), err: nil, seen: nil},
	})

	result, err := sut.Dispatch(context.Background(), &qdata.Query{Plan: joinPlan("job")})
	require.NoError(t, err)
	require.Len(t, result.GetTable().GetRows(), 1)

	row := result.GetTable().GetRows()[0].GetValues()

	sample, ok := qdata.AttrGet(row, "value")
	require.True(t, ok, "sample keeps the value column")
	assert.InDelta(t, 0.5, sample.GetDoubleValue(), 1e-9)

	label, ok := qdata.AttrGet(row, "label_value")
	require.True(t, ok, "the original value label is preserved under label_value")
	assert.Equal(t, "user-label", label.GetStringValue())
}

func TestFailClosed(t *testing.T) {
	t.Parallel()

	metrics := &fakeHandler{result: metricResult(1), err: nil, seen: nil}
	logs := &fakeHandler{result: logResult("api"), err: nil, seen: nil}
	full := map[qdata.Signal]pipeline.Handler{qdata.SignalMetrics: metrics, qdata.SignalLogs: logs}

	t.Run("no plan", func(t *testing.T) {
		t.Parallel()

		_, err := crosssignalconnector.New(full).Dispatch(context.Background(), &qdata.Query{})
		require.Error(t, err)
	})

	t.Run("missing target for single signal", func(t *testing.T) {
		t.Parallel()

		sut := crosssignalconnector.New(map[qdata.Signal]pipeline.Handler{})
		_, err := sut.Dispatch(context.Background(),
			&qdata.Query{Plan: qdata.Plan(qdata.SelectNode(qdata.SignalMetrics, nil))})
		require.Error(t, err)
	})

	t.Run("multi-signal plan that is not a top-level binary", func(t *testing.T) {
		t.Parallel()

		plan := qdata.Plan(qdata.FunctionNode("vector", []*qdata.Node{
			qdata.SelectNode(qdata.SignalMetrics, nil),
			qdata.SelectNode(qdata.SignalLogs, nil),
		}))
		_, err := crosssignalconnector.New(full).Dispatch(context.Background(), &qdata.Query{Plan: plan})
		require.Error(t, err)
	})

	t.Run("no join key", func(t *testing.T) {
		t.Parallel()

		regionMetrics := &fakeHandler{result: metricByLabel("region", "eu"), err: nil, seen: nil}
		zoneLogs := &fakeHandler{result: logByLabel("zone", "a"), err: nil, seen: nil}
		sut := crosssignalconnector.New(map[qdata.Signal]pipeline.Handler{
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
