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

	// The sort must carry unmapped_type so an index missing the time field does
	// not fail the whole search.
	sort, ok := got["sort"].([]any)
	require.True(t, ok, "request must carry a sort")
	require.NotEmpty(t, sort)
	sortField, ok := sort[0].(map[string]any)["@timestamp"].(map[string]any)
	require.True(t, ok, "sort must be keyed on the time field")
	assert.Equal(t, "date", sortField["unmapped_type"], "sort must set unmapped_type to tolerate missing fields")
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

func TestNumericEpochMillisTimestamp(t *testing.T) {
	t.Parallel()

	// @timestamp indexed as an epoch-millis number (ES's default numeric date
	// format) must be honored, not silently replaced with time.Now().
	body := `{"hits":{"hits":[{"_index":"i","_id":"1","_source":{"@timestamp":1767322845000,"message":"hi"}}]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), luceneQuery("*"))
	require.NoError(t, err)

	records := result.GetLogs().GetRecords()
	require.Len(t, records, 1)
	assert.Equal(t, int64(1767322845000), records[0].GetStart().AsTime().UnixMilli(),
		"numeric epoch-millis @timestamp should map to the record time")
}

func TestLargeIntegerFieldKeepsPrecision(t *testing.T) {
	t.Parallel()

	// A long field beyond float64's exact-integer range must not be rounded.
	body := `{"hits":{"hits":[{"_index":"i","_id":"1","_source":` +
		`{"@timestamp":"2026-01-02T03:04:05Z","message":"hi","event_id":9223372036854775807}}]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), luceneQuery("*"))
	require.NoError(t, err)

	records := result.GetLogs().GetRecords()
	require.Len(t, records, 1)

	eventID, ok := qdata.AttrGet(records[0].GetAttributes(), "event_id")
	require.True(t, ok)
	assert.Equal(t, "9223372036854775807", eventID.GetStringValue(),
		"large integer must keep full precision, not round through float64")
}

func TestNestedTraceAndSpanID(t *testing.T) {
	t.Parallel()

	// ECS nests trace.id / span.id as objects; both the nested and flat forms
	// should populate the record.
	body := `{"hits":{"hits":[{"_index":"i","_id":"1","_source":` +
		`{"@timestamp":"2026-01-02T03:04:05Z","message":"hi",` +
		`"trace":{"id":"abc123"},"span.id":"def456"}}]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), luceneQuery("*"))
	require.NoError(t, err)

	records := result.GetLogs().GetRecords()
	require.Len(t, records, 1)
	assert.Equal(t, "abc123", records[0].GetTraceId(), "nested trace.id should be resolved")
	assert.Equal(t, "def456", records[0].GetSpanId(), "flat span.id should be resolved")
}

func TestPartialResultsSurfaceAsWarnings(t *testing.T) {
	t.Parallel()

	// A 200 response that timed out with failed shards must still parse, and the
	// partial-result signals must surface as feedback warnings.
	body := `{"timed_out":true,"_shards":{"total":5,"successful":3,"failed":2},` +
		`"hits":{"hits":[{"_index":"i","_id":"1","_source":{"@timestamp":"2026-01-02T03:04:05Z","message":"hi"}}]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), luceneQuery("*"))
	require.NoError(t, err)

	require.Len(t, result.GetLogs().GetRecords(), 1, "hits should still be returned")

	notifications := result.GetFeedback().GetNotifications()
	require.Len(t, notifications, 2, "timed_out and shard failure should both warn")

	codes := []string{notifications[0].GetCode(), notifications[1].GetCode()}
	assert.Contains(t, codes, "upstream_timeout")
	assert.Contains(t, codes, "shard_failure")
}

func TestFullResultsHaveNoWarnings(t *testing.T) {
	t.Parallel()

	body := `{"timed_out":false,"_shards":{"total":5,"successful":5,"failed":0},` +
		`"hits":{"hits":[{"_index":"i","_id":"1","_source":{"@timestamp":"2026-01-02T03:04:05Z","message":"hi"}}]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), luceneQuery("*"))
	require.NoError(t, err)

	assert.Empty(t, result.GetFeedback().GetNotifications(), "a complete response must not warn")
}
