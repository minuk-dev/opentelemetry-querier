// Package elasticsearchdispatcher renders a qdata Query to the Elasticsearch
// _search API, executes it against an upstream, and parses the JSON response
// back into a qdata Result. It is the storage-facing stage of the pipeline for
// logs stored in Elasticsearch, alongside the lokidispatcher (Loki) and the
// prometheusdispatcher (metrics).
package elasticsearchdispatcher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
const DefaultEndpoint = "http://localhost:9200"

// DefaultIndex is the index (or index pattern) searched when unset.
const DefaultIndex = "_all"

// DefaultTimeField is the document field carrying the record timestamp when the
// config leaves it unset; "@timestamp" is the Elastic Common Schema default.
const DefaultTimeField = "@timestamp"

// DefaultSize bounds the number of hits requested when unset.
const DefaultSize = 100

// defaultTimeout bounds each upstream request when the config leaves it unset.
const defaultTimeout = 30 * time.Second

// Config configures the upstream Elasticsearch.
type Config struct {
	// Endpoint is the upstream base URL, e.g. http://localhost:9200.
	Endpoint string `mapstructure:"endpoint"`
	// Index is the index or index pattern to search; defaults to _all.
	Index string `mapstructure:"index"`
	// TimeField is the document field carrying the record timestamp.
	TimeField string `mapstructure:"time_field"`
	// Size caps the number of hits returned; defaults to 100.
	Size int `mapstructure:"size"`
	// Timeout bounds each upstream request; defaults to 30s.
	Timeout time.Duration `mapstructure:"timeout"`
	// Username and Password enable HTTP basic auth when set.
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
}

// Dispatcher talks to an upstream Elasticsearch.
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

	if cfg.Index == "" {
		cfg.Index = DefaultIndex
	}

	if cfg.TimeField == "" {
		cfg.TimeField = DefaultTimeField
	}

	if cfg.Size == 0 {
		cfg.Size = DefaultSize
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

// Dispatch executes the query and returns a logs result.
func (d *Dispatcher) Dispatch(ctx context.Context, query *qdata.Query) (*qdata.Result, error) {
	body, err := d.buildBody(query)
	if err != nil {
		return nil, err
	}

	endpoint := strings.TrimRight(d.cfg.Endpoint, "/") + "/" + d.cfg.Index + "/_search"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, qerror.New(qerror.CodeInternal, "elasticsearchdispatcher: build request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	if d.cfg.Username != "" {
		req.SetBasicAuth(d.cfg.Username, d.cfg.Password)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, qerror.New(qerror.CodeUnavailable, "elasticsearchdispatcher: upstream request: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, qerror.New(qerror.CodeUnavailable, "elasticsearchdispatcher: read upstream: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, qerror.New(
			qerror.CodeUnavailable,
			"elasticsearchdispatcher: upstream status %d: %s", resp.StatusCode, string(payload),
		)
	}

	return d.parseResponse(payload)
}

// queryClause renders the query's structured plan to an Elasticsearch query
// object. The plan is the query (design note #10, Phase 3); a query without one
// is rejected.
func queryClause(query *qdata.Query) (map[string]any, error) {
	plan := query.GetPlan()
	if plan == nil {
		return nil, qerror.New(qerror.CodeInvalidArgument, "elasticsearchdispatcher: query has no plan")
	}

	return planToESQuery(plan)
}

// buildBody encodes the _search request body: the plan-derived query, bounded by
// the query's time range on the configured time field, sorted newest-first and
// capped at the configured size.
func (d *Dispatcher) buildBody(query *qdata.Query) ([]byte, error) {
	filter, err := queryClause(query)
	if err != nil {
		return nil, err
	}

	must := []any{filter}

	if rng := timeRange(query, d.cfg.TimeField); rng != nil {
		must = append(must, rng)
	}

	// unmapped_type keeps the sort from erroring when the time field is missing
	// from an index in the pattern (ES treats an unmapped field as empty instead
	// of failing the whole search).
	sortField := map[string]any{"order": "desc", "unmapped_type": "date"}

	body := map[string]any{
		"size":  d.cfg.Size,
		"query": map[string]any{"bool": map[string]any{"must": must}},
		"sort":  []any{map[string]any{d.cfg.TimeField: sortField}},
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, qerror.New(qerror.CodeInternal, "elasticsearchdispatcher: encode request: %v", err)
	}

	return encoded, nil
}

// timeRange builds a range filter on timeField from the query's bounds, or nil
// when no bound is set.
func timeRange(query *qdata.Query, timeField string) map[string]any {
	bounds := map[string]any{}

	if start := query.GetRange().GetStart(); start != nil && !start.AsTime().IsZero() {
		bounds["gte"] = start.AsTime().UTC().Format(time.RFC3339Nano)
	}

	if end := query.GetRange().GetEnd(); end != nil && !end.AsTime().IsZero() {
		bounds["lte"] = end.AsTime().UTC().Format(time.RFC3339Nano)
	}

	if len(bounds) == 0 {
		return nil
	}

	return map[string]any{"range": map[string]any{timeField: bounds}}
}

// ---- Elasticsearch JSON response model ----
//
// Elasticsearch hit metadata uses leading-underscore field names (_index, _id,
// _source) that no struct tag can express in camelCase, so each hit is decoded
// as a raw map and its fields pulled out by key.

// esHits mirrors the "hits" object of an _search response.
type esHits struct {
	Hits []map[string]json.RawMessage `json:"hits"`
}

// parseResponse converts an Elasticsearch _search body into a qdata logs Result.
// A 200 response can still be partial (some shards failed, or the search timed
// out); those are surfaced as feedback warnings so the truncated result is not
// mistaken for a complete one.
//
// The response is decoded via a generic envelope rather than a tagged struct
// because the partial-result fields use snake_case / underscore names that no
// camelCase struct tag can express. For reference, the relevant _search shape is:
//
//	{
//	  "timed_out": false,
//	  "_shards": { "total": 5, "successful": 5, "failed": 0 },
//	  "hits": { "hits": [ { "_index": ..., "_id": ..., "_source": {...} } ] }
//	}
func (d *Dispatcher) parseResponse(body []byte) (*qdata.Result, error) {
	var envelope map[string]json.RawMessage

	err := json.Unmarshal(body, &envelope)
	if err != nil {
		return nil, qerror.New(qerror.CodeUnavailable, "elasticsearchdispatcher: decode response: %v", err)
	}

	var hits esHits

	_ = json.Unmarshal(envelope["hits"], &hits)

	logs := &qdata.Logs{}
	for _, hit := range hits.Hits {
		logs.Records = append(logs.Records, d.hitToRecord(hit))
	}

	result := &qdata.Result{Signal: qdata.SignalLogs, Data: &qdatav1.Result_Logs{Logs: logs}}
	surfacePartialResults(result, envelope)

	return result, nil
}

// surfacePartialResults emits feedback warnings for the _search partial-result
// signals (timed_out, _shards.failed), which a 200 response can still carry.
func surfacePartialResults(result *qdata.Result, envelope map[string]json.RawMessage) {
	var timedOut bool

	_ = json.Unmarshal(envelope["timed_out"], &timedOut)

	if timedOut {
		qdata.Warn(result, "upstream_timeout",
			"elasticsearch reported the search timed out; results may be partial", "elasticsearch")
	}

	// _shards inner fields are camelCase-safe, so a small tagged struct is fine.
	var shards struct {
		Total  int `json:"total"`
		Failed int `json:"failed"`
	}

	_ = json.Unmarshal(envelope["_shards"], &shards)

	if shards.Failed > 0 {
		qdata.Warn(result, "shard_failure",
			fmt.Sprintf("elasticsearch reported %d of %d shards failed; results may be partial",
				shards.Failed, shards.Total),
			"elasticsearch")
	}
}

// hitToRecord maps one search hit to a LogRecord: the timestamp comes from the
// configured time field, the body from a "message" field when present (else the
// raw source), and every source field becomes an attribute.
func (d *Dispatcher) hitToRecord(hit map[string]json.RawMessage) *qdata.LogRecord {
	rawSource := hit["_source"]
	source := decodeSource(rawSource)

	attrs := &qdata.KeyValueList{}
	for key, value := range source {
		qdata.AttrPutString(attrs, key, stringify(value))
	}

	qdata.AttrPutString(attrs, "_index", rawString(hit["_index"]))
	qdata.AttrPutString(attrs, "_id", rawString(hit["_id"]))

	stamp := recordTime(source, d.cfg.TimeField)

	return &qdata.LogRecord{
		Start:       stamp,
		End:         stamp,
		Severity:    qdatav1.Severity_SEVERITY_UNSPECIFIED,
		Body:        qdata.Str(recordBody(source, rawSource)),
		TraceId:     nestedString(source, "trace", "id"),
		SpanId:      nestedString(source, "span", "id"),
		Fingerprint: "",
		Sampling:    0,
		Attributes:  attrs,
	}
}

// decodeSource unmarshals a hit's _source, preserving numeric precision:
// UseNumber keeps JSON numbers as json.Number rather than float64, so a large
// `long` field is not silently rounded through a float.
func decodeSource(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var source map[string]any

	err := decoder.Decode(&source)
	if err != nil {
		return nil
	}

	return source
}

// nestedString resolves parent.child from the source, accepting both the ECS
// nested-object form ({"trace":{"id":...}}) and a flat dotted key
// ("trace.id"), returning "" when neither is present.
func nestedString(source map[string]any, parent, child string) string {
	if nested, ok := source[parent].(map[string]any); ok {
		return stringify(nested[child])
	}

	return stringify(source[parent+"."+child])
}

// rawString decodes a raw JSON string field, returning "" when absent or not a
// string.
func rawString(raw json.RawMessage) string {
	var out string

	_ = json.Unmarshal(raw, &out)

	return out
}

// recordTime reads the configured time field, falling back to the current time
// when absent or unparseable.
func recordTime(source map[string]any, timeField string) *timestamppb.Timestamp {
	parsed, ok := parseTimeValue(source[timeField])
	if !ok {
		return timestamppb.New(time.Now())
	}

	return timestamppb.New(parsed)
}

// parseTimeValue reads a time field as either an RFC3339(Nano) string or a
// numeric epoch value. A bare number is interpreted as epoch milliseconds, which
// is Elasticsearch's default numeric date format (epoch_millis).
func parseTimeValue(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, typed)
		if err != nil {
			return time.Time{}, false
		}

		return parsed, true
	case json.Number:
		millis, err := typed.Float64()
		if err != nil {
			return time.Time{}, false
		}

		return time.UnixMilli(int64(millis)), true
	default:
		return time.Time{}, false
	}
}

// recordBody prefers a "message" field for the log line, falling back to the raw
// JSON source so no information is lost.
func recordBody(source map[string]any, raw json.RawMessage) string {
	if message, ok := source["message"].(string); ok {
		return message
	}

	return string(raw)
}

// stringify renders a JSON-decoded value as a string for use as an attribute or
// body. Strings pass through; other scalars are formatted; composites are
// re-encoded as JSON.
func stringify(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}

		return string(encoded)
	}
}
