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
	// The Prometheus HTTP API only speaks PromQL. Reject any other dialect
	// rather than ship its text to an endpoint that would mis-parse it — the
	// dispatcher's half of the dialect contract (design note #10, Phase 0).
	if dialect := qdata.QueryDialect(query); dialect != qdata.DialectPromQL {
		return nil, qerror.New(qerror.CodeInvalidArgument,
			"promdispatcher: cannot execute %q dialect against the Prometheus API", dialect)
	}

	endpoint, form := d.buildRequest(query)

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
		return nil, qerror.New(qerror.CodeUnavailable, "promdispatcher: upstream request: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, qerror.New(qerror.CodeUnavailable, "promdispatcher: read upstream: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, qerror.New(
			qerror.CodeUnavailable,
			"promdispatcher: upstream status %d: %s", resp.StatusCode, string(body),
		)
	}

	return parseResponse(body)
}

// buildRequest picks the instant vs range endpoint and encodes the form.
func (d *Dispatcher) buildRequest(query *qdata.Query) (string, url.Values) {
	base := strings.TrimRight(d.cfg.Endpoint, "/")
	form := url.Values{}
	form.Set("query", query.GetExpr())

	if query.GetContext() == qdata.ContextRange {
		form.Set("start", formatTime(query.GetRange().GetStart().AsTime()))
		form.Set("end", formatTime(query.GetRange().GetEnd().AsTime()))
		form.Set("step", formatStep(query.GetStep().AsDuration()))

		return base + "/api/v1/query_range", form
	}

	// Instant: evaluate at the range end, or now when unset.
	evalAt := query.GetRange().GetEnd().AsTime()
	if evalAt.IsZero() || evalAt.Unix() <= 0 {
		evalAt = time.Now()
	}

	form.Set("time", formatTime(evalAt))

	return base + "/api/v1/query", form
}

func formatTime(instant time.Time) string {
	return strconv.FormatFloat(float64(instant.UnixNano())/nanosPerSecond, 'f', fullPrecision, floatBitSize)
}

func formatStep(step time.Duration) string {
	if step <= 0 {
		step = time.Minute
	}

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
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"`
	Values [][]any           `json:"values"`
}

// parseResponse converts a Prometheus JSON body into a qdata metrics Result.
func parseResponse(body []byte) (*qdata.Result, error) {
	var resp promResponse

	err := json.Unmarshal(body, &resp)
	if err != nil {
		return nil, qerror.New(qerror.CodeUnavailable, "promdispatcher: decode response: %v", err)
	}

	if resp.Status != "success" {
		return nil, qerror.New(
			qerror.CodeInvalidArgument,
			"promdispatcher: upstream error (%s): %s", resp.ErrorType, resp.Error,
		)
	}

	result := &qdata.Result{Signal: qdata.SignalMetrics}

	switch resp.Data.ResultType {
	case "vector", "matrix":
		var samples []promSample

		err := json.Unmarshal(resp.Data.Result, &samples)
		if err != nil {
			return nil, qerror.New(qerror.CodeUnavailable, "promdispatcher: decode %s: %v", resp.Data.ResultType, err)
		}

		result.Data = &qdatav1.Result_Metrics{Metrics: samplesToMetrics(samples)}
	default:
		// scalar/string results carry no series; return an empty metrics payload.
		result.Data = &qdatav1.Result_Metrics{Metrics: &qdata.Metrics{}}
	}

	for _, warning := range resp.Warnings {
		qdata.Warn(result, "upstream_warning", warning, "prometheus")
	}

	return result, nil
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
