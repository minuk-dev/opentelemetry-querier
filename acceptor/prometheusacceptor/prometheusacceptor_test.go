package prometheusacceptor_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/minuk-dev/opentelemetry-querier/acceptor/prometheusacceptor"
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
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
			Values [][]any           `json:"values"`
		} `json:"result"`
	} `json:"data"`
	Warnings  []string `json:"warnings"`
	ErrorType string   `json:"errorType"`
}

func metricsResult() *qdata.Result {
	series := &qdata.MetricSeries{
		Name:       "up",
		Type:       qdata.MetricUnknown,
		Attributes: qdata.NewAttrs("job", qdata.Str("api")),
		Points: []*qdata.MetricPoint{{
			End:   timestamppb.New(time.Unix(1700000000, 0)),
			Value: qdata.Double(1),
		}},
	}

	return &qdata.Result{
		Signal: qdata.SignalMetrics,
		Data:   &qdatav1.Result_Metrics{Metrics: &qdata.Metrics{Series: []*qdata.MetricSeries{series}}},
	}
}

func serve(t *testing.T, handler pipeline.Handler) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(prometheusacceptor.New(prometheusacceptor.Config{Endpoint: ""}, handler).Handler())
	t.Cleanup(server.Close)

	return server
}

func getJSON(t *testing.T, url string) (int, apiResponse) {
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

	err = json.NewDecoder(resp.Body).Decode(&decoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	return resp.StatusCode, decoded
}

func TestInstantQueryReturnsVector(t *testing.T) {
	t.Parallel()

	server := serve(t, stubHandler{result: metricsResult(), err: nil})

	status, body := getJSON(t, server.URL+"/api/v1/query?query=up")
	if status != http.StatusOK || body.Status != "success" {
		t.Fatalf("status = %d %q", status, body.Status)
	}

	if body.Data.ResultType != "vector" {
		t.Fatalf("resultType = %q, want vector", body.Data.ResultType)
	}

	if len(body.Data.Result) != 1 || body.Data.Result[0].Metric["__name__"] != "up" {
		t.Fatalf("result = %+v", body.Data.Result)
	}

	if len(body.Data.Result[0].Value) != 2 {
		t.Fatalf("value = %+v, want [ts, val]", body.Data.Result[0].Value)
	}
}

func TestRangeQueryReturnsMatrix(t *testing.T) {
	t.Parallel()

	server := serve(t, stubHandler{result: metricsResult(), err: nil})

	status, body := getJSON(t, server.URL+"/api/v1/query_range?query=up&start=1700000000&end=1700000100&step=60")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}

	if body.Data.ResultType != "matrix" {
		t.Fatalf("resultType = %q, want matrix", body.Data.ResultType)
	}

	if len(body.Data.Result) != 1 || len(body.Data.Result[0].Values) != 1 {
		t.Fatalf("result = %+v", body.Data.Result)
	}
}

func TestMissingQueryIsBadRequest(t *testing.T) {
	t.Parallel()

	server := serve(t, stubHandler{result: nil, err: nil})

	status, body := getJSON(t, server.URL+"/api/v1/query")
	if status != http.StatusBadRequest || body.Status != "error" {
		t.Fatalf("status = %d %q, want 400 error", status, body.Status)
	}
}

func TestHandlerErrorMapsStatus(t *testing.T) {
	t.Parallel()

	server := serve(t, stubHandler{result: nil, err: qerror.New(qerror.CodeUnauthenticated, "nope")})

	status, body := getJSON(t, server.URL+"/api/v1/query?query=up")
	if status != http.StatusUnauthorized || body.ErrorType != "unauthorized" {
		t.Fatalf("status = %d errorType = %q, want 401 unauthorized", status, body.ErrorType)
	}
}
