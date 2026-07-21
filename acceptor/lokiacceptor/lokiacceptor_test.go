package lokiacceptor_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/minuk-dev/opentelemetry-querier/acceptor/lokiacceptor"
	qdatav1 "github.com/minuk-dev/opentelemetry-querier/gen/qdata/v1"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

type stubHandler struct {
	result *qdata.Result
	err    error
}

func (s stubHandler) Handle(_ context.Context, _ *qdata.Query) (*qdata.Result, error) {
	return s.result, s.err
}

type apiResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Metric map[string]string `json:"metric"`
			Values [][]any           `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

// logsResult returns two records sharing one label set, so they must collapse
// into a single stream.
func logsResult() *qdata.Result {
	attrs := qdata.NewAttrs("job", qdata.Str("api"))
	stamp := timestamppb.New(time.Unix(0, 1700000000000000000))

	records := []*qdata.LogRecord{
		{Start: stamp, End: stamp, Body: qdata.Str("hello"), Attributes: attrs},
		{Start: stamp, End: stamp, Body: qdata.Str("world"), Attributes: attrs},
	}

	return &qdata.Result{
		Signal: qdata.SignalLogs,
		Data:   &qdatav1.Result_Logs{Logs: &qdata.Logs{Records: records}},
	}
}

func metricsResult() *qdata.Result {
	series := &qdata.MetricSeries{
		Name:       "rate",
		Type:       qdata.MetricUnknown,
		Attributes: qdata.NewAttrs("level", qdata.Str("error")),
		Points: []*qdata.MetricPoint{{
			End:   timestamppb.New(time.Unix(1700000000, 0)),
			Value: qdata.Double(2),
		}},
	}

	return &qdata.Result{
		Signal: qdata.SignalMetrics,
		Data:   &qdatav1.Result_Metrics{Metrics: &qdata.Metrics{Series: []*qdata.MetricSeries{series}}},
	}
}

func serve(t *testing.T, handler pipeline.Handler) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(lokiacceptor.New(lokiacceptor.Config{Endpoint: ""}, handler).Handler())
	t.Cleanup(server.Close)

	return server
}

func get(t *testing.T, url string) (int, apiResponse) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	var decoded apiResponse

	// Error responses are plain text; only success bodies are JSON envelopes.
	if resp.StatusCode == http.StatusOK {
		err = json.NewDecoder(resp.Body).Decode(&decoded)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
	}

	return resp.StatusCode, decoded
}

func TestInstantQueryReturnsStreams(t *testing.T) {
	t.Parallel()

	server := serve(t, stubHandler{result: logsResult(), err: nil})

	status, body := get(t, server.URL+`/loki/api/v1/query?query={job="api"}`)
	if status != http.StatusOK || body.Status != "success" {
		t.Fatalf("status = %d %q", status, body.Status)
	}

	if body.Data.ResultType != "streams" {
		t.Fatalf("resultType = %q, want streams", body.Data.ResultType)
	}

	// Two records, one label set -> one stream with two entries.
	if len(body.Data.Result) != 1 {
		t.Fatalf("streams = %d, want 1 (records share labels)", len(body.Data.Result))
	}

	if body.Data.Result[0].Stream["job"] != "api" {
		t.Fatalf("stream labels = %+v", body.Data.Result[0].Stream)
	}

	if len(body.Data.Result[0].Values) != 2 {
		t.Fatalf("entries = %d, want 2", len(body.Data.Result[0].Values))
	}
}

func TestMetricsResultReturnsMatrix(t *testing.T) {
	t.Parallel()

	server := serve(t, stubHandler{result: metricsResult(), err: nil})

	url := server.URL + `/loki/api/v1/query_range?query=rate({job="api"}[5m])&start=1700000000&end=1700000100`

	status, body := get(t, url)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}

	if body.Data.ResultType != "matrix" {
		t.Fatalf("resultType = %q, want matrix", body.Data.ResultType)
	}

	if len(body.Data.Result) != 1 || body.Data.Result[0].Metric["level"] != "error" {
		t.Fatalf("result = %+v", body.Data.Result)
	}
}

func TestMissingQueryIsBadRequest(t *testing.T) {
	t.Parallel()

	server := serve(t, stubHandler{result: nil, err: nil})

	status, _ := get(t, server.URL+"/loki/api/v1/query")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
}

func TestHandlerErrorMapsStatus(t *testing.T) {
	t.Parallel()

	server := serve(t, stubHandler{result: nil, err: qerror.New(qerror.CodeUnauthenticated, "nope")})

	status, _ := get(t, server.URL+`/loki/api/v1/query?query={job="api"}`)
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", status)
	}
}
