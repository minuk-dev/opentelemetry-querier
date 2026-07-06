// Package promdispatcher renders a qdata Query to the Prometheus HTTP query API,
// executes it against an upstream, and parses the JSON response back into a
// qdata Result. It is the storage-facing stage of the pipeline.
package promdispatcher

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	qdatav1 "github.com/minuk-dev/opentelemetry-querier/gen/qdata/v1"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// DefaultTenantHeader is forwarded to the upstream to scope multi-tenant reads.
const DefaultTenantHeader = "X-Scope-OrgID"

// Config configures the upstream Prometheus.
type Config struct {
	// Endpoint is the upstream base URL, e.g. http://localhost:9090.
	Endpoint string `yaml:"endpoint"`
	// TenantHeader is the header used to forward the resolved tenant id.
	TenantHeader string `yaml:"tenant_header"`
	// Timeout bounds each upstream request; defaults to 30s.
	Timeout time.Duration `yaml:"timeout"`
}

// Dispatcher talks to an upstream Prometheus.
type Dispatcher struct {
	dispatcher.Base
	cfg    Config
	client *http.Client
}

// New builds the dispatcher.
func New(cfg Config) *Dispatcher {
	if cfg.TenantHeader == "" {
		cfg.TenantHeader = DefaultTenantHeader
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &Dispatcher{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout}}
}

func (d *Dispatcher) Name() string { return "prometheus" }

// Dispatch executes the query and returns a metrics result.
func (d *Dispatcher) Dispatch(ctx context.Context, q *qdata.Query) (*qdata.Result, error) {
	endpoint, form := d.buildRequest(q)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, qerror.New(qerror.CodeInternal, "promdispatcher: build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if q.GetTenantId() != "" {
		req.Header.Set(d.cfg.TenantHeader, q.GetTenantId())
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, qerror.New(qerror.CodeUnavailable, "promdispatcher: upstream request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, qerror.New(qerror.CodeUnavailable, "promdispatcher: read upstream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, qerror.New(qerror.CodeUnavailable, "promdispatcher: upstream status %d: %s", resp.StatusCode, string(body))
	}

	return parseResponse(body)
}

// buildRequest picks the instant vs range endpoint and encodes the form.
func (d *Dispatcher) buildRequest(q *qdata.Query) (string, url.Values) {
	base := strings.TrimRight(d.cfg.Endpoint, "/")
	form := url.Values{}
	form.Set("query", q.GetExpr())

	if q.GetContext() == qdata.ContextRange {
		form.Set("start", formatTime(q.GetRange().GetStart().AsTime()))
		form.Set("end", formatTime(q.GetRange().GetEnd().AsTime()))
		form.Set("step", formatStep(q.GetStep().AsDuration()))
		return base + "/api/v1/query_range", form
	}

	// Instant: evaluate at the range end, or now when unset.
	evalAt := q.GetRange().GetEnd().AsTime()
	if evalAt.IsZero() || evalAt.Unix() <= 0 {
		evalAt = time.Now()
	}
	form.Set("time", formatTime(evalAt))
	return base + "/api/v1/query", form
}

func formatTime(t time.Time) string {
	return strconv.FormatFloat(float64(t.UnixNano())/1e9, 'f', -1, 64)
}

func formatStep(d time.Duration) string {
	if d <= 0 {
		d = time.Minute
	}
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64)
}

// ---- Prometheus JSON response model ----

type promResponse struct {
	Status    string   `json:"status"`
	ErrorType string   `json:"errorType"`
	Error     string   `json:"error"`
	Data      promData `json:"data"`
	Warnings  []string `json:"warnings"`
}

type promData struct {
	ResultType string          `json:"resultType"`
	Result     json.RawMessage `json:"result"`
}

type promSample struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"`
	Values [][]any           `json:"values"`
}

// parseResponse converts a Prometheus JSON body into a qdata metrics Result.
func parseResponse(body []byte) (*qdata.Result, error) {
	var pr promResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		return nil, qerror.New(qerror.CodeUnavailable, "promdispatcher: decode response: %v", err)
	}
	if pr.Status != "success" {
		return nil, qerror.New(qerror.CodeInvalidArgument, "promdispatcher: upstream error (%s): %s", pr.ErrorType, pr.Error)
	}

	result := &qdata.Result{Signal: qdata.SignalMetrics}

	switch pr.Data.ResultType {
	case "vector", "matrix":
		var samples []promSample
		if err := json.Unmarshal(pr.Data.Result, &samples); err != nil {
			return nil, qerror.New(qerror.CodeUnavailable, "promdispatcher: decode %s: %v", pr.Data.ResultType, err)
		}
		result.Data = &qdatav1.Result_Metrics{Metrics: samplesToMetrics(samples)}
	default:
		// scalar/string results carry no series; return an empty metrics payload.
		result.Data = &qdatav1.Result_Metrics{Metrics: &qdata.Metrics{}}
	}

	for _, w := range pr.Warnings {
		qdata.Warn(result, "upstream_warning", w, "prometheus")
	}
	return result, nil
}

func samplesToMetrics(samples []promSample) *qdata.Metrics {
	metrics := &qdata.Metrics{}
	for _, s := range samples {
		series := &qdata.MetricSeries{
			// Prometheus does not report the metric type; per the QLSWG spec this
			// is UNKNOWN rather than an assumed GAUGE.
			Type:       qdata.MetricUnknown,
			Attributes: &qdata.KeyValueList{},
		}
		for k, v := range s.Metric {
			if k == "__name__" {
				series.Name = v
				continue
			}
			qdata.AttrPutString(series.Attributes, k, v)
		}
		if len(s.Value) == 2 {
			if pt := sampleToPoint(s.Value); pt != nil {
				series.Points = append(series.Points, pt)
			}
		}
		for _, val := range s.Values {
			if pt := sampleToPoint(val); pt != nil {
				series.Points = append(series.Points, pt)
			}
		}
		metrics.Series = append(metrics.Series, series)
	}
	return metrics
}

// sampleToPoint converts a [unixSeconds, "value"] pair into a MetricPoint.
func sampleToPoint(pair []any) *qdata.MetricPoint {
	if len(pair) != 2 {
		return nil
	}
	tsFloat, ok := pair[0].(float64)
	if !ok {
		return nil
	}
	str, ok := pair[1].(string)
	if !ok {
		return nil
	}
	f, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return nil
	}
	ts := timestamppb.New(time.Unix(0, int64(tsFloat*1e9)))
	return &qdata.MetricPoint{Start: ts, End: ts, Value: qdata.Double(f)}
}
