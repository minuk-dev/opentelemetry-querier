package elasticsearchacceptor_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/acceptor/elasticsearchacceptor"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher/elasticsearchdispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
)

// maxBodyBytes bounds the request body the upstream reads (gosec G107/G110).
const maxBodyBytes = 1 << 20

// TestEndToEndLuceneThroughPipeline drives a well-known Lucene query through the
// full path — Elasticsearch acceptor -> (no processors) -> Elasticsearch
// dispatcher -> httptest upstream — and asserts the upstream receives the
// correctly rendered _search query and the client gets the parsed hits (#57).
func TestEndToEndLuceneThroughPipeline(t *testing.T) {
	t.Parallel()

	var gotBody string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		gotBody = string(raw)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":{"total":{"value":1},"hits":[` +
			`{"_index":"logs","_id":"1","_source":{"level":"error","message":"boom"}}]}}`))
	}))
	defer upstream.Close()

	pipe := pipeline.New("logs", nil, elasticsearchdispatcher.New(elasticsearchdispatcher.Config{
		Endpoint: upstream.URL, Index: "", TimeField: "", Size: 0, Timeout: 0, Username: "", Password: "",
	}))
	acc := elasticsearchacceptor.New(elasticsearchacceptor.Config{Endpoint: ""}, pipe)

	front := httptest.NewServer(acc.Handler())
	defer front.Close()

	reqBody := bytes.NewReader([]byte(`{"query":{"query_string":{"query":"level:error"}}}`))

	resp, err := http.Post(front.URL+"/_search", "application/json", reqBody) //nolint:noctx // test client
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)

	assert.Contains(t, gotBody, "level", "the dispatcher renders the field into the _search query")
	assert.Contains(t, gotBody, "error", "the dispatcher renders the value into the _search query")

	var decoded struct {
		Hits struct {
			Hits []json.RawMessage `json:"hits"`
		} `json:"hits"`
	}
	require.NoError(t, json.Unmarshal(body, &decoded))
	require.Len(t, decoded.Hits.Hits, 1, "the upstream hit round-trips to the client")
	assert.Contains(t, string(decoded.Hits.Hits[0]), "boom")
}
