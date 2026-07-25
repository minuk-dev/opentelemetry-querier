package crosssignalconnector_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/connector/crosssignalconnector"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher/lokidispatcher"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher/prometheusdispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// maxBodyBytes bounds the form body the test upstreams parse (gosec G120).
const maxBodyBytes = 1 << 20

// recordingProcessor counts the queries it processes so the test can prove the
// per-signal pipeline's processors run on each sub-query (not bypassed).
type recordingProcessor struct {
	processor.Base

	queries int
}

func (p *recordingProcessor) ProcessQuery(_ context.Context, _ *qdata.Query) error {
	p.queries++

	return nil
}

// TestEndToEndMetricsLogsJoinThroughPipelines drives the real Prometheus and Loki
// dispatchers behind full per-signal pipelines and joins their responses into a
// Table. It also asserts each target pipeline's processors run on the sub-query,
// which the old dispatcher-based approach bypassed (issue #24 / connector rework).
func TestEndToEndMetricsLogsJoinThroughPipelines(t *testing.T) {
	t.Parallel()

	promSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		assert.Equal(t, `{__name__="up"}`, r.FormValue("query"), "prometheus receives the rendered PromQL")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector",` +
			`"result":[{"metric":{"__name__":"up","job":"api"},"value":[1700000000,"0.5"]}]}}`))
	}))
	defer promSrv.Close()

	lokiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		assert.Equal(t, `{job="api"}`, r.FormValue("query"), "loki receives the rendered LogQL")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams",` +
			`"result":[{"stream":{"job":"api"},"values":[["1700000000000000000","boom"]]}]}}`))
	}))
	defer lokiSrv.Close()

	metricsProc := &recordingProcessor{Base: processor.Base{}, queries: 0}
	metricsPipe := pipeline.New("metrics", []processor.Processor{metricsProc},
		prometheusdispatcher.New(prometheusdispatcher.Config{Endpoint: promSrv.URL, TenantHeader: "", Timeout: 0}))
	logsPipe := pipeline.New("logs", nil, lokidispatcher.New(
		lokidispatcher.Config{Endpoint: lokiSrv.URL, TenantHeader: "", Timeout: 0, Limit: 0, Direction: ""}))

	sut := crosssignalconnector.New(map[qdata.Signal]pipeline.Handler{
		qdata.SignalMetrics: metricsPipe,
		qdata.SignalLogs:    logsPipe,
	})

	lhs := qdata.SelectNode(qdata.SignalMetrics, qdata.LeafPredicate(
		&qdata.LabelMatcher{Name: "__name__", Op: qdata.MatchEqual, Value: "up"}))
	rhs := qdata.SelectNode(qdata.SignalLogs, qdata.LeafPredicate(
		&qdata.LabelMatcher{Name: "job", Op: qdata.MatchEqual, Value: "api"}))
	plan := qdata.Plan(qdata.BinaryNode(qdata.BinAnd, lhs, rhs, &qdata.VectorMatch{On: []string{"job"}}))

	result, err := sut.Dispatch(context.Background(), &qdata.Query{Plan: plan})
	require.NoError(t, err)

	table := result.GetTable()
	require.NotNil(t, table, "cross-signal result is a Table")
	require.NoError(t, qdata.ValidateTable(table))
	require.Len(t, table.GetRows(), 1, "the metrics series joins the log stream on job=api")

	row := table.GetRows()[0].GetValues()
	assertColumn(t, row, "job", "api")
	assertColumn(t, row, "__name__", "up")
	assertColumn(t, row, "body", "boom")

	value, ok := qdata.AttrGet(row, "value")
	require.True(t, ok, "joined row carries the metric value")
	assert.InDelta(t, 0.5, value.GetDoubleValue(), 1e-9)

	assert.Equal(t, 1, metricsProc.queries, "the metrics pipeline's processors ran on the sub-query")
}

func assertColumn(t *testing.T, row *qdata.KeyValueList, column, want string) {
	t.Helper()

	value, ok := qdata.AttrGet(row, column)
	require.True(t, ok, "row has column %q", column)
	assert.Equal(t, want, value.GetStringValue())
}
