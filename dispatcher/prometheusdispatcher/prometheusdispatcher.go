// Package prometheusdispatcher renders a qdata Query to the Prometheus HTTP query API,
// executes it against an upstream, and parses the JSON response back into a
// qdata Result. It is the storage-facing stage of the pipeline.
package prometheusdispatcher

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

// defaultTimeout bounds each upstream request when the config leaves it unset.
const defaultTimeout = 30 * time.Second

// maxResponseBytes bounds the decoded body before JSON parsing.
const maxResponseBytes = 16 << 20

const (
	// nanosPerSecond converts between Prometheus float seconds and Go nanos.
	nanosPerSecond = 1e9
	// floatBitSize is the bit size used for float parsing/formatting.
	floatBitSize = 64
	// fullPrecision asks strconv to use the minimal digits round-tripping the value.
	fullPrecision = -1
	// sampleFields is the length of a Prometheus [timestamp, value] sample pair.
	sampleFields = 2
)

// Config configures the upstream Prometheus.
type Config struct {
	MaxResponseBytes int64 `mapstructure:"max_response_bytes"`
	MaxSeries        int   `mapstructure:"max_series"`
	MaxSamples       int   `mapstructure:"max_samples"`
	// Endpoint is the upstream base URL, e.g. http://localhost:9090.
	Endpoint string `mapstructure:"endpoint"`
	// TenantHeader is the header used to forward the resolved tenant id.
	TenantHeader string `mapstructure:"tenant_header"`
	// Timeout bounds each upstream request; defaults to 30s.
	Timeout time.Duration `mapstructure:"timeout"`
}

// Dispatcher talks to an upstream Prometheus.
type Dispatcher struct {
	dispatcher.Base

	cfg    Config
	client *http.Client
}

// New builds the dispatcher.
func New(cfg Config) *Dispatcher {
	if cfg.MaxResponseBytes < 0 || cfg.MaxResponseBytes > maxResponseBytes {
		cfg.MaxResponseBytes = 0
	}

	if cfg.TenantHeader == "" {
		cfg.TenantHeader = DefaultTenantHeader
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}

	return &Dispatcher{
		Base:   dispatcher.Base{},
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
	}
}

// Dispatch executes the query and returns a metrics result.
func (d *Dispatcher) Dispatch(ctx context.Context, query *qdata.Query) (*qdata.Result, error) {
	err := qdata.ValidateQueryTime(query)
	if err != nil || query.GetContext() == qdata.ContextStreaming {
		return nil, qerror.New(qerror.CodeInvalidArgument, "promdispatcher: invalid time context: %v", err)
	}

	promQL, err := queryText(query)
	if err != nil {
		return nil, err
	}

	endpoint, form := d.buildRequest(query, promQL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, qerror.New(qerror.CodeInternal, "promdispatcher: build request: %v", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if tenantID := qdata.TenantID(query); tenantID != "" {
		req.Header.Set(d.cfg.TenantHeader, tenantID)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, qerror.UpstreamTransportError(err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := d.readResponseBody(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, qerror.UpstreamHTTPError(resp.StatusCode, body)
	}

	return d.parseResponse(body)
}

func (d *Dispatcher) readResponseBody(reader io.Reader) ([]byte, error) {
	limit := d.cfg.MaxResponseBytes
	if limit == 0 {
		limit = maxResponseBytes
	}

	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, qerror.UpstreamTransportError(err)
	}

	if int64(len(body)) > limit {
		if d.cfg.MaxResponseBytes > 0 {
			return nil, qerror.New(qerror.CodeResourceExhausted, "result exceeds max_response_bytes (%d)", limit)
		}

		return nil, qerror.New(qerror.CodeUnavailable, "upstream response exceeds %d bytes", maxResponseBytes)
	}

	return body, nil
}

// queryText renders the query's structured plan to PromQL. The plan is the query
// (design note #10, Phase 3); a query without one is rejected.
func queryText(query *qdata.Query) (string, error) {
	plan := query.GetPlan()
	if plan == nil {
		return "", qerror.New(qerror.CodeInvalidArgument, "promdispatcher: query has no plan")
	}

	return planToPromQL(plan)
}

// buildRequest picks the instant vs range endpoint and encodes the form.
func (d *Dispatcher) buildRequest(query *qdata.Query, promQL string) (string, url.Values) {
	base := strings.TrimRight(d.cfg.Endpoint, "/")
	form := url.Values{}
	form.Set("query", promQL)

	if query.GetContext() == qdata.ContextRange {
		form.Set("start", formatTime(query.GetRange().GetStart().AsTime()))
		form.Set("end", formatTime(query.GetRange().GetEnd().AsTime()))
		form.Set("step", formatStep(query.GetStep().AsDuration()))

		return base + "/api/v1/query_range", form
	}

	// Instant: evaluate at the range end, or now when unset.
	evalAt := query.GetRange().GetEnd().AsTime()
	if query.GetRange().GetEnd() == nil {
		evalAt = time.Now()
	}

	form.Set("time", formatTime(evalAt))

	return base + "/api/v1/query", form
}

func formatTime(instant time.Time) string {
	seconds := float64(instant.Unix()) + float64(instant.Nanosecond())/nanosPerSecond

	return strconv.FormatFloat(seconds, 'f', fullPrecision, floatBitSize)
}

func formatStep(step time.Duration) string {
	return strconv.FormatFloat(step.Seconds(), 'f', fullPrecision, floatBitSize)
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
	Metric     map[string]string `json:"metric"`
	Value      []any             `json:"value"`
	Values     [][]any           `json:"values"`
	Histogram  json.RawMessage   `json:"histogram"`
	Histograms json.RawMessage   `json:"histograms"`
}

// parseResponse converts a Prometheus JSON body into a qdata metrics Result.
func (d *Dispatcher) parseResponse(body []byte) (*qdata.Result, error) {
	var resp promResponse

	err := json.Unmarshal(body, &resp)
	if err != nil {
		return nil, qerror.New(qerror.CodeUnavailable, "promdispatcher: decode response: %v", err)
	}

	if resp.Status != "success" {
		return nil, qerror.UpstreamHTTPError(http.StatusOK, body)
	}

	result := &qdata.Result{Signal: qdata.SignalMetrics}

	switch resp.Data.ResultType {
	case "vector", "matrix":
		var samples []promSample

		err := json.Unmarshal(resp.Data.Result, &samples)
		if err != nil {
			return nil, qerror.New(qerror.CodeUnavailable, "promdispatcher: decode %s: %v", resp.Data.ResultType, err)
		}

		err = d.checkSamples(samples)
		if err != nil {
			return nil, err
		}

		result.Data = &qdatav1.Result_Metrics{Metrics: samplesToMetrics(samples)}
	default:
		return nil, qerror.New(qerror.CodeUnavailable, "promdispatcher: unsupported result type %q", resp.Data.ResultType)
	}

	for _, warning := range resp.Warnings {
		qdata.Warn(result, "upstream_warning", warning, "prometheus")
	}

	return result, nil
}

func (d *Dispatcher) checkSamples(samples []promSample) error {
	if d.cfg.MaxSeries > 0 && len(samples) > d.cfg.MaxSeries {
		return qerror.New(qerror.CodeResourceExhausted, "result exceeds max_series (%d)", d.cfg.MaxSeries)
	}

	points := 0
	for _, sample := range samples {
		points += len(sample.Values)
		if len(sample.Value) > 0 {
			points++
		}

		if d.cfg.MaxSamples > 0 && points > d.cfg.MaxSamples {
			return qerror.New(qerror.CodeResourceExhausted, "result exceeds max_samples (%d)", d.cfg.MaxSamples)
		}

		if len(sample.Histogram) > 0 || len(sample.Histograms) > 0 {
			return qerror.New(qerror.CodeUnavailable, "promdispatcher: native histogram results are unsupported")
		}
	}

	return nil
}

func samplesToMetrics(samples []promSample) *qdata.Metrics {
	metrics := &qdata.Metrics{}

	for _, sample := range samples {
		// Prometheus does not report the metric type; per the QLSWG spec this is
		// UNKNOWN rather than an assumed GAUGE.
		series := &qdata.MetricSeries{
			Name:                "",
			Type:                qdata.MetricUnknown,
			Attributes:          &qdata.KeyValueList{},
			TemporalAggregation: "",
			GroupAggregation:    "",
			Step:                nil,
			TemporalBoundaries:  nil,
			Points:              nil,
		}

		for key, value := range sample.Metric {
			if key == "__name__" {
				series.Name = value

				continue
			}

			qdata.AttrPutString(series.Attributes, key, value)
		}

		if len(sample.Value) == sampleFields {
			if point := sampleToPoint(sample.Value); point != nil {
				series.Points = append(series.Points, point)
			}
		}

		for _, pair := range sample.Values {
			if point := sampleToPoint(pair); point != nil {
				series.Points = append(series.Points, point)
			}
		}

		metrics.Series = append(metrics.Series, series)
	}

	return metrics
}

// sampleToPoint converts a [unixSeconds, "value"] pair into a MetricPoint.
func sampleToPoint(pair []any) *qdata.MetricPoint {
	if len(pair) != sampleFields {
		return nil
	}

	seconds, ok := pair[0].(float64)
	if !ok {
		return nil
	}

	raw, ok := pair[1].(string)
	if !ok {
		return nil
	}

	parsed, err := strconv.ParseFloat(raw, floatBitSize)
	if err != nil {
		return nil
	}

	stamp := timestamppb.New(time.Unix(0, int64(seconds*nanosPerSecond)))

	return &qdata.MetricPoint{Start: stamp, End: stamp, Value: qdata.Double(parsed), Exemplars: nil}
}
