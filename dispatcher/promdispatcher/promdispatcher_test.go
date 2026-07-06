package promdispatcher

import (
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

func TestParseResponse_Vector(t *testing.T) {
	body := []byte(`{
		"status":"success",
		"data":{
			"resultType":"vector",
			"result":[
				{"metric":{"__name__":"up","job":"api"},"value":[1700000000,"1"]}
			]
		}
	}`)

	res, err := parseResponse(body)
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	series := res.GetMetrics().GetSeries()
	if len(series) != 1 {
		t.Fatalf("series = %d, want 1", len(series))
	}
	s := series[0]
	if s.GetName() != "up" {
		t.Fatalf("name = %q, want up", s.GetName())
	}
	if s.GetType() != qdata.MetricUnknown {
		t.Fatalf("type = %v, want UNKNOWN (Prometheus is type-less)", s.GetType())
	}
	if v, ok := qdata.AttrGet(s.GetAttributes(), "job"); !ok || v.GetStringValue() != "api" {
		t.Fatalf("job attribute missing or wrong: %v %v", v, ok)
	}
	if len(s.GetPoints()) != 1 || s.GetPoints()[0].GetValue().GetDoubleValue() != 1 {
		t.Fatalf("points = %v, want single value 1", s.GetPoints())
	}
}

func TestParseResponse_Matrix(t *testing.T) {
	body := []byte(`{
		"status":"success",
		"data":{
			"resultType":"matrix",
			"result":[
				{"metric":{"__name__":"rps"},"values":[[1700000000,"1"],[1700000060,"2"]]}
			]
		}
	}`)

	res, err := parseResponse(body)
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	series := res.GetMetrics().GetSeries()
	if len(series) != 1 || len(series[0].GetPoints()) != 2 {
		t.Fatalf("want 1 series with 2 points, got %v", series)
	}
}

func TestParseResponse_UpstreamError(t *testing.T) {
	body := []byte(`{"status":"error","errorType":"bad_data","error":"parse error"}`)
	if _, err := parseResponse(body); err == nil {
		t.Fatal("expected error for status=error response")
	}
}

func TestParseResponse_Warnings(t *testing.T) {
	body := []byte(`{
		"status":"success",
		"warnings":["something odd"],
		"data":{"resultType":"vector","result":[]}
	}`)
	res, err := parseResponse(body)
	if err != nil {
		t.Fatalf("parseResponse: %v", err)
	}
	if n := len(res.GetFeedback().GetNotifications()); n != 1 {
		t.Fatalf("notifications = %d, want 1 (upstream warning surfaced via feedback channel)", n)
	}
}
