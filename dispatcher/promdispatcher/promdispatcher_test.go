package promdispatcher_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher/promdispatcher"
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

func newDispatcher(endpoint string) *promdispatcher.Dispatcher {
	return promdispatcher.New(promdispatcher.Config{Endpoint: endpoint, TenantHeader: "", Timeout: 0})
}

func TestDispatchVector(t *testing.T) {
	t.Parallel()

	body := `{"status":"success","data":{"resultType":"vector","result":[` +
		`{"metric":{"__name__":"up","job":"api"},"value":[1700000000,"1"]}]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), &qdata.Query{Expr: "up"})
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

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), &qdata.Query{Expr: "rps"})
	require.NoError(t, err)

	series := result.GetMetrics().GetSeries()
	require.Len(t, series, 1)
	assert.Len(t, series[0].GetPoints(), 2)
}

func TestDispatchSurfacesWarnings(t *testing.T) {
	t.Parallel()

	body := `{"status":"success","warnings":["something odd"],"data":{"resultType":"vector","result":[]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), &qdata.Query{Expr: "up"})
	require.NoError(t, err)

	assert.Len(t, result.GetFeedback().GetNotifications(), 1,
		"upstream warning should surface via the feedback channel")
}

func TestDispatchUpstreamError(t *testing.T) {
	t.Parallel()

	server := newServer(t, http.StatusInternalServerError, "boom")

	_, err := newDispatcher(server.URL).Dispatch(context.Background(), &qdata.Query{Expr: "up"})
	require.Error(t, err, "upstream 500 should be an error")
}

func TestDispatchRejectsNonPromQLDialect(t *testing.T) {
	t.Parallel()

	// The upstream must never be contacted: the dialect guard has to fail closed
	// before any request is built or sent.
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("upstream must not be called for an unsupported dialect")
	}))
	t.Cleanup(server.Close)

	_, err := newDispatcher(server.URL).Dispatch(
		context.Background(),
		&qdata.Query{Expr: `{job="x"}`, Dialect: qdata.DialectLogQL},
	)
	require.Error(t, err, "non-PromQL dialect must be rejected")
}
