package lokiacceptor_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			Value  []any             `json:"value"`
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

func TestInstantMetricQueryReturnsVector(t *testing.T) {
	t.Parallel()

	// An instant metric query must be answered as a "vector" with a single
	// "value" per series, not a "matrix" with "values".
	server := serve(t, stubHandler{result: metricsResult(), err: nil})

	status, body := get(t, server.URL+`/loki/api/v1/query?query=sum(rate({job="api"}[5m]))`)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}

	if body.Data.ResultType != "vector" {
		t.Fatalf("resultType = %q, want vector", body.Data.ResultType)
	}

	if len(body.Data.Result) != 1 || len(body.Data.Result[0].Value) != 2 {
		t.Fatalf("want one series with a single [ts,val] value, got %+v", body.Data.Result)
	}

	if len(body.Data.Result[0].Values) != 0 {
		t.Fatalf("vector series must not carry a matrix 'values' array: %+v", body.Data.Result[0].Values)
	}
}

// capturingHandler records the query it receives so a test can assert how the
// request was parsed.
type capturingHandler struct {
	result *qdata.Result
	seen   *qdata.Query
}

func (h *capturingHandler) Handle(_ context.Context, query *qdata.Query) (*qdata.Result, error) {
	h.seen = query

	return h.result, nil
}

func TestSecondPrecisionTimestampsParsedAsSeconds(t *testing.T) {
	t.Parallel()

	// A 10-digit integer is Unix seconds (Loki's rule); it must not be read as
	// nanoseconds (which would land in 1970).
	handler := &capturingHandler{result: logsResult(), seen: nil}
	server := serve(t, handler)

	url := server.URL + `/loki/api/v1/query_range?query={job="api"}&start=1700000000&end=1700000100`

	status, _ := get(t, url)
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}

	if handler.seen == nil {
		t.Fatal("handler never called")
	}

	gotStart := handler.seen.GetRange().GetStart().AsTime().UTC()
	if gotStart.Year() != 2023 {
		t.Fatalf("start = %s, want a 2023 instant (1700000000 is Unix seconds)", gotStart)
	}
}

// captureLogQL sends a LogQL instant query and returns the parsed plan's root
// node captured from the pipeline.
func captureLogQL(t *testing.T, logQL string) *qdata.Node {
	t.Helper()

	handler := &capturingHandler{result: logsResult(), seen: nil}
	server := serve(t, handler)

	status, _ := get(t, server.URL+"/loki/api/v1/query?query="+url.QueryEscape(logQL))
	require.Equal(t, http.StatusOK, status)
	require.NotNil(t, handler.seen, "handler was called")

	return handler.seen.GetPlan().GetRoot()
}

func planMatchers(t *testing.T, filter *qdata.Predicate) map[string]string {
	t.Helper()

	matchers, ok := qdata.FlattenConjunction([]*qdata.Predicate{filter})
	require.True(t, ok, "filter should flatten to a conjunction")

	out := map[string]string{}
	for _, matcher := range matchers {
		out[matcher.GetName()] = matcher.GetValue()
	}

	return out
}

func TestParsesStreamSelectorAndLineFilter(t *testing.T) {
	t.Parallel()

	sel := captureLogQL(t, `{job="api", level=~"error|warn"} |= "boom"`).GetSelect()
	require.NotNil(t, sel, "root should be a Select")
	assert.Equal(t, qdata.SignalLogs, sel.GetSignal())

	labels := planMatchers(t, sel.GetFilter())
	assert.Equal(t, "api", labels["job"])
	assert.Equal(t, "error|warn", labels["level"])

	lines := sel.GetLine()
	require.Len(t, lines, 1)
	assert.Equal(t, qdata.MatchEqual, lines[0].GetOp())
	assert.Equal(t, "boom", lines[0].GetValue())
}

func TestParsesRateToTimeAgg(t *testing.T) {
	t.Parallel()

	timeAgg := captureLogQL(t, `rate({job="api"}[5m])`).GetTimeAgg()
	require.NotNil(t, timeAgg, "root should be a TimeAgg")
	assert.Equal(t, qdata.TimeAggRate, timeAgg.GetOp())
	assert.Equal(t, 5*time.Minute, timeAgg.GetWindow().AsDuration())
	assert.Equal(t, "api", planMatchers(t, timeAgg.GetInput().GetSelect().GetFilter())["job"])
}

func TestParsesSumByRate(t *testing.T) {
	t.Parallel()

	aggregate := captureLogQL(t, `sum by(job) (rate({job="api"}[5m]))`).GetAggregate()
	require.NotNil(t, aggregate, "root should be an Aggregate")
	assert.Equal(t, qdata.AggSum, aggregate.GetOp())
	assert.Equal(t, []string{"job"}, aggregate.GetBy())
	assert.NotNil(t, aggregate.GetInput().GetTimeAgg(), "aggregate input should be a TimeAgg")
}

func TestRejectsUnsupportedLogQL(t *testing.T) {
	t.Parallel()

	// A label pipeline (| json) is outside the supported subset -> 400.
	handler := &capturingHandler{result: logsResult(), seen: nil}
	server := serve(t, handler)

	status, _ := get(t, server.URL+"/loki/api/v1/query?query="+url.QueryEscape(`{job="api"} | json`))
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Nil(t, handler.seen, "pipeline must not run for an unparseable query")
}
