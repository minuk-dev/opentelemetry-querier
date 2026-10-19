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

	var gotQuery, gotLimit, gotDirection string

	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Body = http.MaxBytesReader(writer, request.Body, maxBodyBytes)
		gotQuery = request.FormValue("query")
		gotLimit, gotDirection = request.FormValue("limit"), request.FormValue("direction")

		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"success","data":{"resultType":"streams",` +
			`"result":[{"stream":{"job":"api"},"values":[["1700000000000000000","boom"]]}]}}`))
	}))
	defer upstream.Close()

	pipe := pipeline.New("logs", nil, lokidispatcher.New(
		lokidispatcher.Config{Endpoint: upstream.URL, TenantHeader: "", Timeout: 0, Limit: 0, Direction: ""}))
	acc := lokiacceptor.New(lokiacceptor.Config{Endpoint: ""}, pipe)

	front := httptest.NewServer(acc.Handler())
	defer front.Close()

	escaped := url.QueryEscape(`{job="api"}`)

	endpoint := front.URL + "/loki/api/v1/query?limit=1&direction=forward&query=" + escaped
	resp, err := http.Get(endpoint) //nolint:noctx // test client
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", body)

	assert.Equal(t, "1", gotLimit)
	assert.Equal(t, "forward", gotDirection)

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

func TestLogRequestOptions(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, options, limit, direction string
		status                          int
	}{
		{name: "forward", options: "limit=1&direction=forward", limit: "1", direction: "forward", status: http.StatusOK},
		{name: "defaults", options: "", limit: "100", direction: "backward", status: http.StatusOK},
		{name: "backward", options: "direction=backward", limit: "100", direction: "backward", status: http.StatusOK},
		{name: "too large", options: "limit=101", limit: "", direction: "", status: http.StatusBadRequest},
		{name: "zero", options: "limit=0", limit: "", direction: "", status: http.StatusBadRequest},
		{name: "negative", options: "limit=-1", limit: "", direction: "", status: http.StatusBadRequest},
		{name: "malformed", options: "limit=NaN", limit: "", direction: "", status: http.StatusBadRequest},
		{name: "invalid direction", options: "direction=sideways", limit: "", direction: "", status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var captured url.Values

			upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				request.Body = http.MaxBytesReader(writer, request.Body, maxBodyBytes)
				_ = request.ParseForm()
				captured = request.Form
				_, _ = writer.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[]}}`))
			}))
			t.Cleanup(upstream.Close)

			pipe := pipeline.New("logs", nil, lokidispatcher.New(lokidispatcher.Config{
				Endpoint: upstream.URL, TenantHeader: "", Timeout: 0, Limit: 100, Direction: "backward",
			}))
			acc := lokiacceptor.New(lokiacceptor.Config{Endpoint: ""}, pipe)
			path := "/loki/api/v1/query_range?query=" + url.QueryEscape(`{job="api"}`) +
				"&start=1700000000&end=1700000060&" + test.options
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
			response := httptest.NewRecorder()
			acc.Handler().ServeHTTP(response, request)
			assert.Equal(t, test.status, response.Code, response.Body.String())
			assert.Equal(t, test.limit, captured.Get("limit"))
			assert.Equal(t, test.direction, captured.Get("direction"))
		})
	}
}
