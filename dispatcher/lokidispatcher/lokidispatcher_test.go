package lokidispatcher_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher/lokidispatcher"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

func newServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	return server
}

func newDispatcher(endpoint string) *lokidispatcher.Dispatcher {
	return lokidispatcher.New(lokidispatcher.Config{
		Endpoint:     endpoint,
		TenantHeader: "",
		Timeout:      0,
		Limit:        0,
		Direction:    "",
	})
}

// logQLQuery builds a logs query whose plan is a simple stream selector; the
// dispatch tests exercise response parsing, so the selector content is
// immaterial (the upstream body is canned).
func logQLQuery() *qdata.Query {
	return &qdata.Query{Plan: qdata.Plan(logsSelect(eq("job", "api")))}
}

func TestDispatchStreams(t *testing.T) {
	t.Parallel()

	body := `{"status":"success","data":{"resultType":"streams","result":[` +
		`{"stream":{"job":"api","level":"info"},"values":[` +
		`["1700000000000000000","hello"],["1700000000000000001","world"]]}]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), logQLQuery())
	require.NoError(t, err)

	assert.Equal(t, qdata.SignalLogs, result.GetSignal())

	records := result.GetLogs().GetRecords()
	require.Len(t, records, 2)
	assert.Equal(t, "hello", records[0].GetBody().GetStringValue())
	assert.Equal(t, int64(1700000000000000000), records[0].GetStart().AsTime().UnixNano())

	value, ok := qdata.AttrGet(records[0].GetAttributes(), "level")
	require.True(t, ok, "stream label should become a log attribute")
	assert.Equal(t, "info", value.GetStringValue())
}

func TestDispatchMetrics(t *testing.T) {
	t.Parallel()

	// LogQL metric queries (e.g. rate()) return a matrix, mapped to metrics.
	body := `{"status":"success","data":{"resultType":"matrix","result":[` +
		`{"metric":{"level":"error"},"values":[[1700000000,"1"],[1700000060,"2"]]}]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(
		context.Background(),
		logQLQuery(),
	)
	require.NoError(t, err)

	assert.Equal(t, qdata.SignalMetrics, result.GetSignal())

	series := result.GetMetrics().GetSeries()
	require.Len(t, series, 1)
	assert.Len(t, series[0].GetPoints(), 2)
}

// captureQuery runs a request against a server that records the `query` form
// value and returns an empty streams body.
func captureQuery(t *testing.T, query *qdata.Query) string {
	t.Helper()

	var got string

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Body = http.MaxBytesReader(writer, request.Body, 1<<20)
		_ = request.ParseForm()
		got = request.Form.Get("query")

		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
	}))
	t.Cleanup(server.Close)

	_, err := newDispatcher(server.URL).Dispatch(context.Background(), query)
	require.NoError(t, err)

	return got
}

// logsSelect builds a logs Select over an AND of the given matchers.
func logsSelect(matchers ...*qdata.LabelMatcher) *qdata.Node {
	preds := make([]*qdata.Predicate, 0, len(matchers))
	for _, matcher := range matchers {
		preds = append(preds, qdata.LeafPredicate(matcher))
	}

	return qdata.SelectNode(qdata.SignalLogs, qdata.BoolPredicate(qdata.BoolAnd, preds...))
}

func eq(name, value string) *qdata.LabelMatcher {
	return &qdata.LabelMatcher{Name: name, Op: qdata.MatchEqual, Value: value}
}

func TestDispatchRendersPlan(t *testing.T) {
	t.Parallel()

	stream := logsSelect(eq("job", "api"))
	withLine := qdata.SelectNode(qdata.SignalLogs,
		qdata.LeafPredicate(eq("job", "api")), qdata.LineFilter(qdata.MatchEqual, "error"))
	rateNode := qdata.TimeAggNode(qdata.TimeAggRate, 5*time.Minute, stream)
	sumByRate := qdata.Plan(qdata.AggregateNode(qdata.AggSum, []string{"job"}, nil, 0, rateNode))

	cases := []struct {
		name string
		plan *qdata.QueryPlan
		want string
	}{
		{"stream selector", qdata.Plan(stream), `{job="api"}`},
		{"line filter", qdata.Plan(withLine), `{job="api"} |= "error"`},
		{"rate over selector", qdata.Plan(rateNode), `rate({job="api"}[300s])`},
		{"sum by rate", sumByRate, `sum by(job)(rate({job="api"}[300s]))`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testCase.want, captureQuery(t, &qdata.Query{Plan: testCase.plan}))
		})
	}
}

func TestDispatchRejectsUnrenderablePlan(t *testing.T) {
	t.Parallel()

	orSelector := qdata.SelectNode(qdata.SignalLogs,
		qdata.BoolPredicate(qdata.BoolOr, qdata.LeafPredicate(eq("a", "1")), qdata.LeafPredicate(eq("b", "2"))))

	cases := map[string]*qdata.QueryPlan{
		"metrics signal": qdata.Plan(qdata.SelectNode(qdata.SignalMetrics, qdata.LeafPredicate(eq("__name__", "up")))),
		"or selector":    qdata.Plan(orSelector),
		"empty selector": qdata.Plan(qdata.SelectNode(qdata.SignalLogs, nil)),
	}

	for name, plan := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("upstream must not be called for an unrenderable plan")
			}))
			t.Cleanup(server.Close)

			_, err := newDispatcher(server.URL).Dispatch(context.Background(), &qdata.Query{Plan: plan})
			require.Error(t, err)
		})
	}
}

func TestDispatchUpstreamError(t *testing.T) {
	t.Parallel()

	server := newServer(t, http.StatusInternalServerError, "boom")

	_, err := newDispatcher(server.URL).Dispatch(context.Background(), logQLQuery())
	require.Error(t, err, "upstream 500 should be an error")
}

func TestDispatchForwardsTenantAndUsesRange(t *testing.T) {
	t.Parallel()

	var (
		gotTenant string
		gotPath   string
	)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotTenant = request.Header.Get(lokidispatcher.DefaultTenantHeader)
		gotPath = request.URL.Path

		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
	}))
	t.Cleanup(server.Close)

	query := logQLQuery()
	query.Context = qdata.ContextRange
	qdata.SetTenantID(query, "acme")

	_, err := newDispatcher(server.URL).Dispatch(context.Background(), query)
	require.NoError(t, err)

	assert.Equal(t, "acme", gotTenant, "resolved tenant must be forwarded upstream")
	assert.Equal(t, "/loki/api/v1/query_range", gotPath, "range context must hit the range endpoint")
}

func TestDispatchStreamsWithStructuredMetadata(t *testing.T) {
	t.Parallel()

	// Loki 3.x entries can carry a third element: an object of structured
	// metadata. It must decode (not error) and merge as per-record attributes.
	body := `{"status":"success","data":{"resultType":"streams","result":[` +
		`{"stream":{"job":"api"},"values":[` +
		`["1700000000000000000","hello",{"trace_id":"abc"}]]}]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), logQLQuery())
	require.NoError(t, err)

	records := result.GetLogs().GetRecords()
	require.Len(t, records, 1)
	assert.Equal(t, "hello", records[0].GetBody().GetStringValue())

	traceID, ok := qdata.AttrGet(records[0].GetAttributes(), "trace_id")
	require.True(t, ok, "structured metadata should become a per-record attribute")
	assert.Equal(t, "abc", traceID.GetStringValue())
}

func TestDispatchStreamRecordsHaveIndependentAttributes(t *testing.T) {
	t.Parallel()

	// Two entries in one stream share labels but must not share the same
	// attribute list: mutating one record's attributes must not affect the other.
	body := `{"status":"success","data":{"resultType":"streams","result":[` +
		`{"stream":{"job":"api"},"values":[` +
		`["1700000000000000000","a"],["1700000000000000001","b"]]}]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), logQLQuery())
	require.NoError(t, err)

	records := result.GetLogs().GetRecords()
	require.Len(t, records, 2)

	// Delete the shared label from the first record only.
	qdata.AttrDelete(records[0].GetAttributes(), "job")

	_, gone := qdata.AttrGet(records[0].GetAttributes(), "job")
	assert.False(t, gone, "label removed from record[0]")

	_, kept := qdata.AttrGet(records[1].GetAttributes(), "job")
	assert.True(t, kept, "record[1] attributes must be independent of record[0]")
}

func TestValidateRejectsBadConfig(t *testing.T) {
	t.Parallel()

	require.Error(t, lokidispatcher.Validate(lokidispatcher.Config{
		Endpoint: "", TenantHeader: "", Timeout: 0, Limit: 0, Direction: "sideways",
	}), "unknown direction must be rejected")

	require.Error(t, lokidispatcher.Validate(lokidispatcher.Config{
		Endpoint: "", TenantHeader: "", Timeout: 0, Limit: -1, Direction: "",
	}), "negative limit must be rejected")

	require.NoError(t, lokidispatcher.Validate(lokidispatcher.Config{
		Endpoint: "", TenantHeader: "", Timeout: 0, Limit: 100, Direction: "backward",
	}), "well-formed config must pass")
}
