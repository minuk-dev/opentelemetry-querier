package elasticsearchacceptor_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/minuk-dev/opentelemetry-querier/acceptor/elasticsearchacceptor"
	qdatav1 "github.com/minuk-dev/opentelemetry-querier/gen/qdata/v1"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// captureHandler records the query it receives and returns a canned result.
type captureHandler struct {
	result *qdata.Result
	err    error
	seen   *qdata.Query
}

func (h *captureHandler) Handle(_ context.Context, query *qdata.Query) (*qdata.Result, error) {
	h.seen = query

	return h.result, h.err
}

func logsResult() *qdata.Result {
	stamp := timestamppb.New(time.Unix(1700000000, 0))
	records := []*qdata.LogRecord{
		{Start: stamp, End: stamp, Body: qdata.Str("hello"), Attributes: qdata.NewAttrs("level", qdata.Str("info"))},
	}

	return &qdata.Result{
		Signal: qdata.SignalLogs,
		Data:   &qdatav1.Result_Logs{Logs: &qdata.Logs{Records: records}},
	}
}

func serve(t *testing.T, handler pipeline.Handler) *httptest.Server {
	t.Helper()

	acc := elasticsearchacceptor.New(elasticsearchacceptor.Config{Endpoint: ""}, handler)
	server := httptest.NewServer(acc.Handler())
	t.Cleanup(server.Close)

	return server
}

// do issues a request and returns the status plus the decoded JSON body (as a
// generic map so the test navigates Elasticsearch's underscore-prefixed fields
// without struct tags).
func do(t *testing.T, method, url, body string) (int, map[string]any) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	decoded := map[string]any{}

	if resp.StatusCode == http.StatusOK {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&decoded))
	}

	return resp.StatusCode, decoded
}

// firstHitSource digs out hits.hits[0]._source from a decoded response.
func firstHitSource(t *testing.T, body map[string]any) map[string]any {
	t.Helper()

	hitsBlock, ok := body["hits"].(map[string]any)
	require.True(t, ok, "response has a hits block: %+v", body)
	hits, ok := hitsBlock["hits"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, hits)

	source, ok := hits[0].(map[string]any)["_source"].(map[string]any)
	require.True(t, ok, "hit has a _source: %+v", hits[0])

	return source
}

// planMatchers flattens the plan's Select filter into a name->value map.
func planMatchers(t *testing.T, query *qdata.Query) map[string]string {
	t.Helper()

	sel := query.GetPlan().GetRoot().GetSelect()
	require.NotNil(t, sel, "plan root should be a Select")

	matchers, ok := qdata.FlattenConjunction([]*qdata.Predicate{sel.GetFilter()})
	require.True(t, ok, "filter should flatten to a conjunction")

	out := map[string]string{}
	for _, matcher := range matchers {
		out[matcher.GetName()] = matcher.GetValue()
	}

	return out
}

// planFilter returns the plan's Select filter predicate.
func planFilter(t *testing.T, query *qdata.Query) *qdata.Predicate {
	t.Helper()

	sel := query.GetPlan().GetRoot().GetSelect()
	require.NotNil(t, sel, "plan root should be a Select")

	return sel.GetFilter()
}

func TestSearchRendersHits(t *testing.T) {
	t.Parallel()

	server := serve(t, &captureHandler{result: logsResult(), err: nil, seen: nil})

	status, body := do(t, http.MethodGet, server.URL+"/logs-*/_search?q=level:info", "")
	require.Equal(t, http.StatusOK, status)

	source := firstHitSource(t, body)
	assert.Equal(t, "hello", source["message"])
	assert.Equal(t, "info", source["level"])
}

func TestQueryParamBecomesLucenePlan(t *testing.T) {
	t.Parallel()

	handler := &captureHandler{result: logsResult(), err: nil, seen: nil}
	server := serve(t, handler)

	_, _ = do(t, http.MethodGet, server.URL+"/logs-*/_search?q=status:500", "")

	require.NotNil(t, handler.seen, "handler was called")
	assert.Equal(t, "500", planMatchers(t, handler.seen)["status"])
}

func TestPostBodyQueryString(t *testing.T) {
	t.Parallel()

	handler := &captureHandler{result: logsResult(), err: nil, seen: nil}
	server := serve(t, handler)

	body := `{"query":{"query_string":{"query":"level:error"}}}`

	status, _ := do(t, http.MethodPost, server.URL+"/logs-*/_search", body)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "error", planMatchers(t, handler.seen)["level"])
}

func TestLuceneBooleanComposition(t *testing.T) {
	t.Parallel()

	handler := &captureHandler{result: logsResult(), err: nil, seen: nil}
	server := serve(t, handler)

	// level:error AND (job:api OR job:web) NOT env:dev
	query := `level:error AND (job:api OR job:web) AND NOT env:dev`
	_, _ = do(t, http.MethodGet, server.URL+"/logs-*/_search?q="+urlEscape(query), "")

	require.NotNil(t, handler.seen)

	// The top level is an AND; it must not flatten because it contains an OR and
	// a NOT, so ValidatePredicate confirms shape and FlattenConjunction fails.
	filter := planFilter(t, handler.seen)
	require.NoError(t, qdata.ValidatePredicate(filter), "parsed filter should be well-formed")

	_, flat := qdata.FlattenConjunction([]*qdata.Predicate{filter})
	assert.False(t, flat, "a filter with OR/NOT must not flatten to a plain conjunction")
}

func TestEmptyQueryDefaultsToMatchAll(t *testing.T) {
	t.Parallel()

	handler := &captureHandler{result: logsResult(), err: nil, seen: nil}
	server := serve(t, handler)

	// No q param and an empty body: a valid match-all search, not an error.
	status, _ := do(t, http.MethodPost, server.URL+"/logs-*/_search", "")
	require.Equal(t, http.StatusOK, status)
	assert.Nil(t, planFilter(t, handler.seen), "match-all is a Select with no filter")
}

func TestHandlerErrorMapsStatus(t *testing.T) {
	t.Parallel()

	server := serve(t, &captureHandler{result: nil, err: qerror.New(qerror.CodeUnauthenticated, "nope"), seen: nil})

	status, _ := do(t, http.MethodGet, server.URL+"/logs-*/_search?q=level:info", "")
	assert.Equal(t, http.StatusUnauthorized, status)
}

func TestUnsupportedQueryDSLIsRejected(t *testing.T) {
	t.Parallel()

	// A match/term/bool query cannot be translated to Lucene; the proxy must 400
	// rather than silently drop the filter and return everything.
	handler := &captureHandler{result: logsResult(), err: nil, seen: nil}
	server := serve(t, handler)

	body := `{"query":{"match":{"message":"error"}}}`

	status, _ := do(t, http.MethodPost, server.URL+"/logs-*/_search", body)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Nil(t, handler.seen, "pipeline must not be invoked for a rejected query")
}

func TestMatchAllBodyIsAccepted(t *testing.T) {
	t.Parallel()

	handler := &captureHandler{result: logsResult(), err: nil, seen: nil}
	server := serve(t, handler)

	status, _ := do(t, http.MethodPost, server.URL+"/logs-*/_search", `{"query":{"match_all":{}}}`)
	require.Equal(t, http.StatusOK, status)
	assert.Nil(t, planFilter(t, handler.seen), "match_all maps to a Select with no filter")
}

func TestOversizedBodyIsRejected(t *testing.T) {
	t.Parallel()

	handler := &captureHandler{result: logsResult(), err: nil, seen: nil}
	server := serve(t, handler)

	// A body beyond the 1 MiB cap must be rejected, not buffered.
	huge := `{"query":{"query_string":{"query":"` + strings.Repeat("a", 2<<20) + `"}}}`

	status, _ := do(t, http.MethodPost, server.URL+"/logs-*/_search", huge)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Nil(t, handler.seen, "pipeline must not be invoked for an oversized body")
}

// urlEscape percent-encodes a query string for use in a URL.
func urlEscape(raw string) string {
	replacer := strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29")

	return replacer.Replace(raw)
}
