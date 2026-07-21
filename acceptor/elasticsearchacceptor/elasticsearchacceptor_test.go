package elasticsearchacceptor_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	decoded := map[string]any{}

	if resp.StatusCode == http.StatusOK {
		err = json.NewDecoder(resp.Body).Decode(&decoded)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
	}

	return resp.StatusCode, decoded
}

// firstHitSource digs out hits.hits[0]._source from a decoded response.
func firstHitSource(t *testing.T, body map[string]any) map[string]any {
	t.Helper()

	hits, ok := body["hits"].(map[string]any)["hits"].([]any)
	if !ok || len(hits) == 0 {
		t.Fatalf("no hits in %+v", body)
	}

	source, ok := hits[0].(map[string]any)["_source"].(map[string]any)
	if !ok {
		t.Fatalf("hit has no _source: %+v", hits[0])
	}

	return source
}

func TestSearchRendersHits(t *testing.T) {
	t.Parallel()

	server := serve(t, &captureHandler{result: logsResult(), err: nil, seen: nil})

	status, body := do(t, http.MethodGet, server.URL+"/logs-*/_search?q=level:info", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}

	source := firstHitSource(t, body)
	if source["message"] != "hello" || source["level"] != "info" {
		t.Fatalf("_source = %+v", source)
	}
}

func TestQueryParamBecomesLuceneExpr(t *testing.T) {
	t.Parallel()

	handler := &captureHandler{result: logsResult(), err: nil, seen: nil}
	server := serve(t, handler)

	_, _ = do(t, http.MethodGet, server.URL+"/logs-*/_search?q=status:500", "")

	if handler.seen == nil {
		t.Fatal("handler never called")
	}

	if got := qdata.QueryDialect(handler.seen); got != qdata.DialectLucene {
		t.Fatalf("dialect = %q, want lucene", got)
	}

	if handler.seen.GetExpr() != "status:500" {
		t.Fatalf("expr = %q, want status:500", handler.seen.GetExpr())
	}
}

func TestPostBodyQueryString(t *testing.T) {
	t.Parallel()

	handler := &captureHandler{result: logsResult(), err: nil, seen: nil}
	server := serve(t, handler)

	body := `{"query":{"query_string":{"query":"level:error"}}}`

	status, _ := do(t, http.MethodPost, server.URL+"/logs-*/_search", body)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}

	if handler.seen.GetExpr() != "level:error" {
		t.Fatalf("expr = %q, want level:error", handler.seen.GetExpr())
	}
}

func TestEmptyQueryDefaultsToMatchAll(t *testing.T) {
	t.Parallel()

	handler := &captureHandler{result: logsResult(), err: nil, seen: nil}
	server := serve(t, handler)

	// No q param and an empty body: a valid match-all search, not an error.
	status, _ := do(t, http.MethodPost, server.URL+"/logs-*/_search", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	if handler.seen.GetExpr() != "*" {
		t.Fatalf("expr = %q, want * (match-all)", handler.seen.GetExpr())
	}
}

func TestHandlerErrorMapsStatus(t *testing.T) {
	t.Parallel()

	server := serve(t, &captureHandler{result: nil, err: qerror.New(qerror.CodeUnauthenticated, "nope"), seen: nil})

	status, _ := do(t, http.MethodGet, server.URL+"/logs-*/_search?q=level:info", "")
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
}
