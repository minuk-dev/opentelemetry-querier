package lokidispatcher_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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

// logQLQuery builds a LogQL query; the dispatcher rejects anything else.
func logQLQuery(expr string) *qdata.Query {
	return &qdata.Query{Expr: expr, Dialect: qdata.DialectLogQL}
}

func TestDispatchStreams(t *testing.T) {
	t.Parallel()

	body := `{"status":"success","data":{"resultType":"streams","result":[` +
		`{"stream":{"job":"api","level":"info"},"values":[` +
		`["1700000000000000000","hello"],["1700000000000000001","world"]]}]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), logQLQuery(`{job="api"}`))
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
		logQLQuery(`rate({job="api"}[5m])`),
	)
	require.NoError(t, err)

	assert.Equal(t, qdata.SignalMetrics, result.GetSignal())

	series := result.GetMetrics().GetSeries()
	require.Len(t, series, 1)
	assert.Len(t, series[0].GetPoints(), 2)
}

func TestDispatchRejectsNonLogQLDialect(t *testing.T) {
	t.Parallel()

	// The upstream must never be contacted: the dialect guard has to fail closed
	// before any request is built or sent.
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("upstream must not be called for an unsupported dialect")
	}))
	t.Cleanup(server.Close)

	// The empty dialect defaults to PromQL, which Loki cannot execute.
	_, err := newDispatcher(server.URL).Dispatch(context.Background(), &qdata.Query{Expr: "up"})
	require.Error(t, err, "non-LogQL dialect must be rejected")
}

func TestDispatchUpstreamError(t *testing.T) {
	t.Parallel()

	server := newServer(t, http.StatusInternalServerError, "boom")

	_, err := newDispatcher(server.URL).Dispatch(context.Background(), logQLQuery(`{job="api"}`))
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

	query := logQLQuery(`{job="api"}`)
	query.Context = qdata.ContextRange
	qdata.SetTenantID(query, "acme")

	_, err := newDispatcher(server.URL).Dispatch(context.Background(), query)
	require.NoError(t, err)

	assert.Equal(t, "acme", gotTenant, "resolved tenant must be forwarded upstream")
	assert.Equal(t, "/loki/api/v1/query_range", gotPath, "range context must hit the range endpoint")
}
