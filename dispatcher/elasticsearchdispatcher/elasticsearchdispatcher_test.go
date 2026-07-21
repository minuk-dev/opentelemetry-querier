package elasticsearchdispatcher_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher/elasticsearchdispatcher"
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

func newDispatcher(endpoint string) *elasticsearchdispatcher.Dispatcher {
	return elasticsearchdispatcher.New(elasticsearchdispatcher.Config{
		Endpoint:  endpoint,
		Index:     "logs-*",
		TimeField: "",
		Size:      0,
		Timeout:   0,
		Username:  "",
		Password:  "",
	})
}

// luceneQuery builds a Lucene query; the dispatcher rejects anything else.
func luceneQuery(expr string) *qdata.Query {
	return &qdata.Query{Expr: expr, Dialect: qdata.DialectLucene}
}

func TestDispatchMapsHitsToLogs(t *testing.T) {
	t.Parallel()

	hit1 := `{"_index":"logs-2026","_id":"a1","_source":` +
		`{"@timestamp":"2026-01-02T03:04:05Z","message":"hello","level":"info"}}`
	hit2 := `{"_index":"logs-2026","_id":"a2","_source":` +
		`{"@timestamp":"2026-01-02T03:04:06Z","message":"world","level":"warn"}}`
	server := newServer(t, http.StatusOK, `{"hits":{"hits":[`+hit1+`,`+hit2+`]}}`)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), luceneQuery("level:info"))
	require.NoError(t, err)

	assert.Equal(t, qdata.SignalLogs, result.GetSignal())

	records := result.GetLogs().GetRecords()
	require.Len(t, records, 2)
	assert.Equal(t, "hello", records[0].GetBody().GetStringValue())
	assert.Equal(t, "2026-01-02T03:04:05Z", records[0].GetStart().AsTime().UTC().Format("2006-01-02T15:04:05Z"))

	level, ok := qdata.AttrGet(records[0].GetAttributes(), "level")
	require.True(t, ok, "source field should become a log attribute")
	assert.Equal(t, "info", level.GetStringValue())
}

func TestDispatchSendsQueryStringAndRange(t *testing.T) {
	t.Parallel()

	var got map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		payload, _ := io.ReadAll(request.Body)
		_ = json.Unmarshal(payload, &got)

		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"hits":{"hits":[]}}`))
	}))
	t.Cleanup(server.Close)

	_, err := newDispatcher(server.URL).Dispatch(context.Background(), luceneQuery("level:error"))
	require.NoError(t, err)

	query, ok := got["query"].(map[string]any)
	require.True(t, ok, "request must carry a query, got %+v", got)
	boolQuery, ok := query["bool"].(map[string]any)
	require.True(t, ok, "query must be a bool query")
	must, ok := boolQuery["must"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, must)

	first, ok := must[0].(map[string]any)
	require.True(t, ok)
	queryString, ok := first["query_string"].(map[string]any)
	require.True(t, ok, "first must clause should be a query_string")
	assert.Equal(t, "level:error", queryString["query"], "expr must be sent as the query_string")
}

func TestDispatchRejectsNonLuceneDialect(t *testing.T) {
	t.Parallel()

	// The upstream must never be contacted: the dialect guard has to fail closed
	// before any request is built or sent.
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("upstream must not be called for an unsupported dialect")
	}))
	t.Cleanup(server.Close)

	// The empty dialect defaults to PromQL, which Elasticsearch cannot execute.
	_, err := newDispatcher(server.URL).Dispatch(context.Background(), &qdata.Query{Expr: "up"})
	require.Error(t, err, "non-Lucene dialect must be rejected")
}

func TestDispatchUpstreamError(t *testing.T) {
	t.Parallel()

	server := newServer(t, http.StatusInternalServerError, "boom")

	_, err := newDispatcher(server.URL).Dispatch(context.Background(), luceneQuery("level:info"))
	require.Error(t, err, "upstream 500 should be an error")
}

func TestDispatchBodyFallsBackToRawSource(t *testing.T) {
	t.Parallel()

	// A hit with no "message" field: the body should carry the raw source so no
	// information is lost.
	body := `{"hits":{"hits":[{"_index":"i","_id":"1","_source":{"@timestamp":"2026-01-02T03:04:05Z","code":500}}]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), luceneQuery("code:500"))
	require.NoError(t, err)

	records := result.GetLogs().GetRecords()
	require.Len(t, records, 1)
	assert.Contains(t, records[0].GetBody().GetStringValue(), "\"code\":500")

	code, ok := qdata.AttrGet(records[0].GetAttributes(), "code")
	require.True(t, ok)
	assert.Equal(t, "500", code.GetStringValue(), "numeric source field should stringify")
}
