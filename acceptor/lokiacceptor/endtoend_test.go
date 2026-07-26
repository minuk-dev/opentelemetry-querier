package lokiacceptor_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/acceptor/lokiacceptor"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher/lokidispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
)

// maxBodyBytes bounds the form body the upstream parses (gosec G120).
const maxBodyBytes = 1 << 20

// TestEndToEndLogQLThroughPipeline drives a well-known LogQL query through the
// full path — Loki acceptor -> (no processors) -> Loki dispatcher -> httptest
// upstream — and asserts the upstream receives the correctly rendered query and
// the client gets the parsed result (issue #57).
func TestEndToEndLogQLThroughPipeline(t *testing.T) {
	t.Parallel()

	var gotQuery string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		gotQuery = r.FormValue("query")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"streams",` +
			`"result":[{"stream":{"job":"api"},"values":[["1700000000000000000","boom"]]}]}}`))
	}))
	defer upstream.Close()

	pipe := pipeline.New("logs", nil, lokidispatcher.New(
		lokidispatcher.Config{Endpoint: upstream.URL, TenantHeader: "", Timeout: 0, Limit: 0, Direction: ""}))
	acc := lokiacceptor.New(lokiacceptor.Config{Endpoint: ""}, pipe)

	front := httptest.NewServer(acc.Handler())
	defer front.Close()

	escaped := url.QueryEscape(`{job="api"}`)

	resp, err := http.Get(front.URL + "/loki/api/v1/query?query=" + escaped) //nolint:noctx // test client
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)

	assert.Equal(t, `{job="api"}`, gotQuery, "the dispatcher renders the parsed LogQL to the upstream")

	var decoded struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string            `json:"resultType"`
			Result     []json.RawMessage `json:"result"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &decoded))

	assert.Equal(t, "success", decoded.Status)
	assert.Equal(t, "streams", decoded.Data.ResultType)
	require.Len(t, decoded.Data.Result, 1, "the upstream stream round-trips to the client")
}
