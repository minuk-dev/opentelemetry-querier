package prometheusacceptor_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/acceptor/prometheusacceptor"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher/prometheusdispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
)

// maxBodyBytes bounds the form body the upstream parses (gosec G120).
const maxBodyBytes = 1 << 20

// TestEndToEndPromQLThroughPipeline drives a well-known PromQL query through the
// full path — Prometheus acceptor -> (no processors) -> Prometheus dispatcher ->
// httptest upstream — and asserts the upstream receives the correctly rendered
// query and the client gets the parsed result (issue #57).
func TestEndToEndPromQLThroughPipeline(t *testing.T) {
	t.Parallel()

	var gotQuery string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		gotQuery = r.FormValue("query")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector",` +
			`"result":[{"metric":{"__name__":"up","job":"api"},"value":[1700000000,"0.5"]}]}}`))
	}))
	defer upstream.Close()

	pipe := pipeline.New("metrics", nil, prometheusdispatcher.New(
		prometheusdispatcher.Config{Endpoint: upstream.URL, TenantHeader: "", Timeout: 0}))
	acc := prometheusacceptor.New(prometheusacceptor.Config{Endpoint: ""}, pipe)

	front := httptest.NewServer(acc.Handler())
	defer front.Close()

	resp, err := http.Get(front.URL + "/api/v1/query?query=up") //nolint:noctx // test client
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)

	assert.Equal(t, `{__name__="up"}`, gotQuery, "the dispatcher renders the parsed PromQL to the upstream")

	var decoded struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Value  []any             `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &decoded))

	assert.Equal(t, "success", decoded.Status)
	assert.Equal(t, "vector", decoded.Data.ResultType)
	require.Len(t, decoded.Data.Result, 1, "the upstream sample round-trips to the client")
	assert.Equal(t, "up", decoded.Data.Result[0].Metric["__name__"])
}
