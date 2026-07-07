package promdispatcher_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher/promdispatcher"
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

func newDispatcher(endpoint string) *promdispatcher.Dispatcher {
	return promdispatcher.New(promdispatcher.Config{Endpoint: endpoint, TenantHeader: "", Timeout: 0})
}

func TestDispatchVector(t *testing.T) {
	t.Parallel()

	body := `{"status":"success","data":{"resultType":"vector","result":[` +
		`{"metric":{"__name__":"up","job":"api"},"value":[1700000000,"1"]}]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), &qdata.Query{Expr: "up"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	series := result.GetMetrics().GetSeries()
	if len(series) != 1 {
		t.Fatalf("series = %d, want 1", len(series))
	}

	if series[0].GetName() != "up" {
		t.Fatalf("name = %q, want up", series[0].GetName())
	}

	if series[0].GetType() != qdata.MetricUnknown {
		t.Fatalf("type = %v, want UNKNOWN (Prometheus is type-less)", series[0].GetType())
	}

	value, ok := qdata.AttrGet(series[0].GetAttributes(), "job")
	if !ok || value.GetStringValue() != "api" {
		t.Fatalf("job attribute missing or wrong")
	}
}

func TestDispatchMatrix(t *testing.T) {
	t.Parallel()

	body := `{"status":"success","data":{"resultType":"matrix","result":[` +
		`{"metric":{"__name__":"rps"},"values":[[1700000000,"1"],[1700000060,"2"]]}]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), &qdata.Query{Expr: "rps"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	series := result.GetMetrics().GetSeries()
	if len(series) != 1 || len(series[0].GetPoints()) != 2 {
		t.Fatalf("want 1 series with 2 points")
	}
}

func TestDispatchSurfacesWarnings(t *testing.T) {
	t.Parallel()

	body := `{"status":"success","warnings":["something odd"],"data":{"resultType":"vector","result":[]}}`
	server := newServer(t, http.StatusOK, body)

	result, err := newDispatcher(server.URL).Dispatch(context.Background(), &qdata.Query{Expr: "up"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	if got := len(result.GetFeedback().GetNotifications()); got != 1 {
		t.Fatalf("notifications = %d, want 1 (upstream warning surfaced via feedback channel)", got)
	}
}

func TestDispatchUpstreamError(t *testing.T) {
	t.Parallel()

	server := newServer(t, http.StatusInternalServerError, "boom")

	_, err := newDispatcher(server.URL).Dispatch(context.Background(), &qdata.Query{Expr: "up"})
	if err == nil {
		t.Fatal("expected error for upstream 500")
	}
}
