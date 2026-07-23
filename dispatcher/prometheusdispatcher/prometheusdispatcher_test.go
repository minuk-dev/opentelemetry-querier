package prometheusdispatcher_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher/prometheusdispatcher"
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

func newDispatcher(endpoint string) *prometheusdispatcher.Dispatcher {
	return prometheusdispatcher.New(prometheusdispatcher.Config{Endpoint: endpoint, TenantHeader: "", Timeout: 0})
}

func TestDispatchVector(t *testing.T) {
	t.Parallel()

	body := `{"status":"success","data":{"resultType":"vector","result":[` +
		`{"metric":{"__name__":"up","job":"api"},"value":[1700000000,"1"]}]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), metricQuery("up"))
	require.NoError(t, err)

	series := result.GetMetrics().GetSeries()
	require.Len(t, series, 1)
	assert.Equal(t, "up", series[0].GetName())
	assert.Equal(t, qdata.MetricUnknown, series[0].GetType(), "Prometheus is type-less")

	value, ok := qdata.AttrGet(series[0].GetAttributes(), "job")
	require.True(t, ok, "job attribute missing")
	assert.Equal(t, "api", value.GetStringValue())
}

func TestDispatchMatrix(t *testing.T) {
	t.Parallel()

	body := `{"status":"success","data":{"resultType":"matrix","result":[` +
		`{"metric":{"__name__":"rps"},"values":[[1700000000,"1"],[1700000060,"2"]]}]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), metricQuery("rps"))
	require.NoError(t, err)

	series := result.GetMetrics().GetSeries()
	require.Len(t, series, 1)
	assert.Len(t, series[0].GetPoints(), 2)
}

func TestDispatchSurfacesWarnings(t *testing.T) {
	t.Parallel()

	body := `{"status":"success","warnings":["something odd"],"data":{"resultType":"vector","result":[]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), metricQuery("up"))
	require.NoError(t, err)

	assert.Len(t, result.GetFeedback().GetNotifications(), 1,
		"upstream warning should surface via the feedback channel")
}

func TestDispatchUpstreamError(t *testing.T) {
	t.Parallel()

	server := newServer(t, http.StatusInternalServerError, "boom")

	_, err := newDispatcher(server.URL).Dispatch(context.Background(), metricQuery("up"))
	require.Error(t, err, "upstream 500 should be an error")
}

// captureQuery runs a request against a server that records the `query` form
// value and returns an empty success body.
func captureQuery(t *testing.T, query *qdata.Query) string {
	t.Helper()

	var got string

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Body = http.MaxBytesReader(writer, request.Body, 1<<20)
		_ = request.ParseForm()
		got = request.Form.Get("query")

		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	t.Cleanup(server.Close)

	_, err := newDispatcher(server.URL).Dispatch(context.Background(), query)
	require.NoError(t, err)

	return got
}

// eq is an equality label matcher.
func eq(name, value string) *qdata.LabelMatcher {
	return &qdata.LabelMatcher{Name: name, Op: qdata.MatchEqual, Value: value}
}

// metricSelect builds a metrics Select over an AND of the given matchers.
func metricSelect(matchers ...*qdata.LabelMatcher) *qdata.Node {
	preds := make([]*qdata.Predicate, 0, len(matchers))
	for _, matcher := range matchers {
		preds = append(preds, qdata.LeafPredicate(matcher))
	}

	return qdata.SelectNode(qdata.SignalMetrics, qdata.BoolPredicate(qdata.BoolAnd, preds...))
}

// metricQuery builds a query whose plan selects the named metric; the dispatch
// tests exercise response parsing, so the selector is minimal.
func metricQuery(name string) *qdata.Query {
	return &qdata.Query{Plan: qdata.Plan(metricSelect(eq("__name__", name)))}
}

func TestDispatchRendersPlan(t *testing.T) {
	t.Parallel()

	httpTotal := metricSelect(eq("__name__", "http_requests_total"))
	rate := qdata.TimeAggNode(qdata.TimeAggRate, 5*time.Minute, httpTotal)
	selector := qdata.Plan(metricSelect(eq("__name__", "up"), eq("job", "api")))
	sumByRate := qdata.Plan(qdata.AggregateNode(qdata.AggSum, []string{"job"}, nil, 0, rate))
	quantile := qdata.Plan(qdata.AggregateNode(qdata.AggQuantile, nil, nil, 0.9, httpTotal))
	binary := qdata.Plan(qdata.BinaryNode(qdata.BinDiv,
		metricSelect(eq("__name__", "a")), metricSelect(eq("__name__", "b")), nil))

	cases := []struct {
		name string
		plan *qdata.QueryPlan
		want string
	}{
		{"selector sorts matchers", selector, `{__name__="up",job="api"}`},
		{"rate over selector", qdata.Plan(rate), `rate({__name__="http_requests_total"}[300s])`},
		{"sum by rate", sumByRate, `sum by(job)(rate({__name__="http_requests_total"}[300s]))`},
		{"quantile leading param", quantile, `quantile(0.9, {__name__="http_requests_total"})`},
		{"binary parenthesizes", binary, `({__name__="a"}) / ({__name__="b"})`},
		{"literal", qdata.Plan(qdata.LiteralNode(1.5)), `1.5`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testCase.want, captureQuery(t, &qdata.Query{Plan: testCase.plan}))
		})
	}
}

func TestDispatchRejectsQueryWithoutPlan(t *testing.T) {
	t.Parallel()

	// The plan is the query; a query with no plan is rejected before the upstream
	// is contacted.
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("upstream must not be called for a query with no plan")
	}))
	t.Cleanup(server.Close)

	_, err := newDispatcher(server.URL).Dispatch(context.Background(), &qdata.Query{})
	require.Error(t, err, "a query without a plan must be rejected")
}

func TestDispatchRejectsUnrenderablePlan(t *testing.T) {
	t.Parallel()

	orSelector := qdata.SelectNode(qdata.SignalMetrics,
		qdata.BoolPredicate(qdata.BoolOr, qdata.LeafPredicate(eq("a", "1")), qdata.LeafPredicate(eq("b", "2"))))

	cases := map[string]*qdata.QueryPlan{
		"logs signal":  qdata.Plan(qdata.SelectNode(qdata.SignalLogs, qdata.LeafPredicate(eq("job", "api")))),
		"or selector":  qdata.Plan(orSelector),
		"empty select": qdata.Plan(qdata.SelectNode(qdata.SignalMetrics, nil)),
	}

	for name, plan := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The upstream must not be hit for a plan PromQL cannot render.
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("upstream must not be called for an unrenderable plan")
			}))
			t.Cleanup(server.Close)

			_, err := newDispatcher(server.URL).Dispatch(context.Background(), &qdata.Query{Plan: plan})
			require.Error(t, err)
		})
	}
}
