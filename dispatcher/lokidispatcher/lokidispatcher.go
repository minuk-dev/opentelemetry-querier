// Package lokidispatcher renders a qdata Query to the Grafana Loki HTTP query
// API, executes it against an upstream, and parses the JSON response back into a
// qdata Result. It is the storage-facing stage of the pipeline for logs, the
// counterpart of the prometheusdispatcher for metrics.
package lokidispatcher

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

// DefaultEndpoint is the upstream base URL when the config leaves it unset.
const DefaultEndpoint = "http://localhost:3100"

// DefaultTenantHeader is forwarded to the upstream to scope multi-tenant reads;
// Loki reads the tenant id from this header.
const DefaultTenantHeader = "X-Scope-OrgID"

// DefaultLimit bounds the number of log entries requested when unset.
const DefaultLimit = 100

// DefaultDirection is the scan direction Loki uses when unset; "backward"
// returns the most recent lines first, matching Loki's own default.
const DefaultDirection = "backward"

// defaultTimeout bounds each upstream request when the config leaves it unset.
const defaultTimeout = 30 * time.Second

const (
	// nanosPerSecond converts between float seconds and Go nanos.
	nanosPerSecond = 1e9
	// floatBitSize is the bit size used for float parsing/formatting.
	floatBitSize = 64
	// fullPrecision asks strconv to use the minimal digits round-tripping the value.
	fullPrecision = -1
	// sampleFields is the length of a Loki metric [timestamp, value] sample pair.
	sampleFields = 2
	// entryFields is the length of a Loki stream [unixNano, line] entry pair.
	entryFields = 2
)

// Config configures the upstream Loki.
type Config struct {
	// Endpoint is the upstream base URL, e.g. http://localhost:3100.
	Endpoint string `mapstructure:"endpoint"`
	// TenantHeader is the header used to forward the resolved tenant id.
	TenantHeader string `mapstructure:"tenant_header"`
	// Timeout bounds each upstream request; defaults to 30s.
	Timeout time.Duration `mapstructure:"timeout"`
	// Limit caps the number of log entries returned; defaults to 100.
	Limit int `mapstructure:"limit"`
	// Direction is the scan direction, "forward" or "backward"; defaults to
	// "backward".
	Direction string `mapstructure:"direction"`
}

// Dispatcher talks to an upstream Loki.
type Dispatcher struct {
	dispatcher.Base

	cfg    Config
	client *http.Client
}

// New builds the dispatcher, applying defaults.
func New(cfg Config) *Dispatcher {
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}

	if cfg.TenantHeader == "" {
		cfg.TenantHeader = DefaultTenantHeader
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}

	if cfg.Limit == 0 {
		cfg.Limit = DefaultLimit
	}

	if cfg.Direction == "" {
		cfg.Direction = DefaultDirection
	}

	return &Dispatcher{
		Base:   dispatcher.Base{},
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
	}
}

// Dispatch executes the query and returns a logs (or metrics) result.
func (d *Dispatcher) Dispatch(ctx context.Context, query *qdata.Query) (*qdata.Result, error) {
	// The Loki HTTP API only speaks LogQL. Reject any other dialect rather than
	// ship its text to an endpoint that would mis-parse it — the dispatcher's
	// half of the dialect contract (design note #10, Phase 0).
	if dialect := qdata.QueryDialect(query); dialect != qdata.DialectLogQL {
		return nil, qerror.New(qerror.CodeInvalidArgument,
			"lokidispatcher: cannot execute %q dialect against the Loki API", dialect)
	}

	endpoint, form := d.buildRequest(query)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, qerror.New(qerror.CodeInternal, "lokidispatcher: build request: %v", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if tenantID := qdata.TenantID(query); tenantID != "" {
		req.Header.Set(d.cfg.TenantHeader, tenantID)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, qerror.New(qerror.CodeUnavailable, "lokidispatcher: upstream request: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, qerror.New(qerror.CodeUnavailable, "lokidispatcher: read upstream: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, qerror.New(
			qerror.CodeUnavailable,
			"lokidispatcher: upstream status %d: %s", resp.StatusCode, string(body),
		)
	}

	return parseResponse(body)
}

// buildRequest picks the instant vs range endpoint and encodes the form.
func (d *Dispatcher) buildRequest(query *qdata.Query) (string, url.Values) {
	base := strings.TrimRight(d.cfg.Endpoint, "/")
	form := url.Values{}
	form.Set("query", query.GetExpr())
	form.Set("limit", strconv.Itoa(d.cfg.Limit))
	form.Set("direction", d.cfg.Direction)

	if query.GetContext() == qdata.ContextRange {
		form.Set("start", formatNano(query.GetRange().GetStart().AsTime()))
		form.Set("end", formatNano(query.GetRange().GetEnd().AsTime()))

		if step := query.GetStep().AsDuration(); step > 0 {
			form.Set("step", formatStep(step))
		}

		return base + "/loki/api/v1/query_range", form
	}

	// Instant: evaluate at the range end, or now when unset.
	evalAt := query.GetRange().GetEnd().AsTime()
	if evalAt.IsZero() || evalAt.Unix() <= 0 {
		evalAt = time.Now()
	}

	form.Set("time", formatNano(evalAt))

	return base + "/loki/api/v1/query", form
}

// formatNano renders an instant as Loki's Unix-nanosecond integer string.
func formatNano(instant time.Time) string {
	return strconv.FormatInt(instant.UnixNano(), 10)
}

func formatStep(step time.Duration) string {
	return strconv.FormatFloat(step.Seconds(), 'f', fullPrecision, floatBitSize)
}

// ---- Loki JSON response model ----

type lokiResponse struct {
	Status string   `json:"status"`
	Data   lokiData `json:"data"`
}

type lokiData struct {
	ResultType string          `json:"resultType"`
	Result     json.RawMessage `json:"result"`
}

type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][]string        `json:"values"`
}

type lokiMetric struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"`
	Values [][]any           `json:"values"`
}

// parseResponse converts a Loki JSON body into a qdata Result: streams become a
// logs payload; matrix/vector (from LogQL metric queries) become metrics.
func parseResponse(body []byte) (*qdata.Result, error) {
	var resp lokiResponse

	err := json.Unmarshal(body, &resp)
	if err != nil {
		return nil, qerror.New(qerror.CodeUnavailable, "lokidispatcher: decode response: %v", err)
	}

	if resp.Status != "success" {
		return nil, qerror.New(qerror.CodeInvalidArgument, "lokidispatcher: upstream status %q", resp.Status)
	}

	switch resp.Data.ResultType {
	case "streams":
		return streamsResult(resp.Data.Result)
	case "matrix", "vector":
		return metricsResult(resp.Data.Result, resp.Data.ResultType)
	default:
		// scalar/unknown carries no series; return an empty logs payload.
		return &qdata.Result{Signal: qdata.SignalLogs, Data: &qdatav1.Result_Logs{Logs: &qdata.Logs{}}}, nil
	}
}

func streamsResult(raw json.RawMessage) (*qdata.Result, error) {
	var streams []lokiStream

	err := json.Unmarshal(raw, &streams)
	if err != nil {
		return nil, qerror.New(qerror.CodeUnavailable, "lokidispatcher: decode streams: %v", err)
	}

	logs := &qdata.Logs{}

	for _, stream := range streams {
		attrs := &qdata.KeyValueList{}
		for key, value := range stream.Stream {
			qdata.AttrPutString(attrs, key, value)
		}

		for _, entry := range stream.Values {
			if record := entryToRecord(entry, attrs); record != nil {
				logs.Records = append(logs.Records, record)
			}
		}
	}

	return &qdata.Result{Signal: qdata.SignalLogs, Data: &qdatav1.Result_Logs{Logs: logs}}, nil
}

// entryToRecord converts a Loki [unixNanoString, line] pair into a LogRecord.
func entryToRecord(entry []string, attrs *qdata.KeyValueList) *qdata.LogRecord {
	if len(entry) != entryFields {
		return nil
	}

	nanos, err := strconv.ParseInt(entry[0], 10, 64)
	if err != nil {
		return nil
	}

	stamp := timestamppb.New(time.Unix(0, nanos))

	return &qdata.LogRecord{
		Start:       stamp,
		End:         stamp,
		Severity:    qdatav1.Severity_SEVERITY_UNSPECIFIED,
		Body:        qdata.Str(entry[1]),
		TraceId:     "",
		SpanId:      "",
		Fingerprint: "",
		Sampling:    0,
		Attributes:  attrs,
	}
}

func metricsResult(raw json.RawMessage, resultType string) (*qdata.Result, error) {
	var samples []lokiMetric

	err := json.Unmarshal(raw, &samples)
	if err != nil {
		return nil, qerror.New(qerror.CodeUnavailable, "lokidispatcher: decode %s: %v", resultType, err)
	}

	metrics := &qdata.Metrics{}

	for _, sample := range samples {
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

	return &qdata.Result{Signal: qdata.SignalMetrics, Data: &qdatav1.Result_Metrics{Metrics: metrics}}, nil
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
