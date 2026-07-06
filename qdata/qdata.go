// Package qdata provides ergonomic aliases and helpers over the generated
// gen/qdata/v1 protobuf types, which are the standardized query & result model
// flowing through the querier pipeline (the equivalent of opentelemetry-collector
// pdata). Generated messages cannot carry hand-written methods from this package,
// so the helpers here are free functions.
package qdata

import (
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	qdatav1 "github.com/minuk-dev/opentelemetry-querier/gen/qdata/v1"
)

// Aliases keep call sites short and let the rest of the codebase depend on the
// qdata package rather than the versioned generated import path.
type (
	Query        = qdatav1.Query
	Result       = qdatav1.Result
	TimeRange    = qdatav1.TimeRange
	Modifier     = qdatav1.Modifier
	LabelMatcher = qdatav1.LabelMatcher
	HeaderValues = qdatav1.HeaderValues

	Value        = qdatav1.Value
	ArrayValue   = qdatav1.ArrayValue
	KeyValue     = qdatav1.KeyValue
	KeyValueList = qdatav1.KeyValueList

	Metrics      = qdatav1.Metrics
	MetricSeries = qdatav1.MetricSeries
	MetricPoint  = qdatav1.MetricPoint
	Exemplar     = qdatav1.Exemplar
	Logs         = qdatav1.Logs
	LogRecord    = qdatav1.LogRecord
	Spans        = qdatav1.Spans
	Span         = qdatav1.Span

	Feedback     = qdatav1.Feedback
	Notification = qdatav1.Notification
)

// Enum re-exports.
const (
	SignalUnspecified = qdatav1.Signal_SIGNAL_UNSPECIFIED
	SignalMetrics     = qdatav1.Signal_SIGNAL_METRICS
	SignalLogs        = qdatav1.Signal_SIGNAL_LOGS
	SignalSpans       = qdatav1.Signal_SIGNAL_SPANS
	SignalProfiles    = qdatav1.Signal_SIGNAL_PROFILES

	ContextInstant   = qdatav1.QueryContext_QUERY_CONTEXT_INSTANT
	ContextRange     = qdatav1.QueryContext_QUERY_CONTEXT_RANGE
	ContextStreaming = qdatav1.QueryContext_QUERY_CONTEXT_STREAMING

	MatchEqual     = qdatav1.MatchOp_MATCH_OP_EQUAL
	MatchNotEqual  = qdatav1.MatchOp_MATCH_OP_NOT_EQUAL
	MatchRegexp    = qdatav1.MatchOp_MATCH_OP_REGEXP
	MatchNotRegexp = qdatav1.MatchOp_MATCH_OP_NOT_REGEXP

	MetricGauge             = qdatav1.MetricType_METRIC_TYPE_GAUGE
	MetricCumulativeCounter = qdatav1.MetricType_METRIC_TYPE_CUMULATIVE_COUNTER
	MetricDeltaCounter      = qdatav1.MetricType_METRIC_TYPE_DELTA_COUNTER
	MetricUnknown           = qdatav1.MetricType_METRIC_TYPE_UNSPECIFIED

	NotifyInfo    = qdatav1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO
	NotifyWarning = qdatav1.NotificationSeverity_NOTIFICATION_SEVERITY_WARNING
	NotifyError   = qdatav1.NotificationSeverity_NOTIFICATION_SEVERITY_ERROR
)

// ---- Value constructors (spec §Data Types) ----

func Double(v float64) *Value { return &Value{Value: &qdatav1.Value_DoubleValue{DoubleValue: v}} }
func Int(v int64) *Value      { return &Value{Value: &qdatav1.Value_IntValue{IntValue: v}} }
func Uint(v uint64) *Value    { return &Value{Value: &qdatav1.Value_UintValue{UintValue: v}} }
func Str(v string) *Value     { return &Value{Value: &qdatav1.Value_StringValue{StringValue: v}} }
func Bool(v bool) *Value      { return &Value{Value: &qdatav1.Value_BoolValue{BoolValue: v}} }
func JSON(raw string) *Value  { return &Value{Value: &qdatav1.Value_JsonValue{JsonValue: raw}} }

func Timestamp(t time.Time) *Value {
	return &Value{Value: &qdatav1.Value_TimestampValue{TimestampValue: timestamppb.New(t)}}
}

func DurationVal(d time.Duration) *Value {
	return &Value{Value: &qdatav1.Value_DurationValue{DurationValue: durationpb.New(d)}}
}

func Array(vs ...*Value) *Value {
	return &Value{Value: &qdatav1.Value_ArrayValue{ArrayValue: &ArrayValue{Values: vs}}}
}

// ---- Attribute helpers (spec §Attributes) ----

// NewAttrs builds a KeyValueList from alternating key, value(*Value) pairs.
func NewAttrs(pairs ...any) *KeyValueList {
	kvl := &KeyValueList{}
	for i := 0; i+1 < len(pairs); i += 2 {
		key, _ := pairs[i].(string)
		val, _ := pairs[i+1].(*Value)
		AttrPut(kvl, key, val)
	}
	return kvl
}

// AttrPut inserts or replaces a key while preserving insertion order.
func AttrPut(kvl *KeyValueList, key string, v *Value) {
	for _, kv := range kvl.Values {
		if kv.Key == key {
			kv.Value = v
			return
		}
	}
	kvl.Values = append(kvl.Values, &KeyValue{Key: key, Value: v})
}

// AttrPutString is a shortcut for a string-valued attribute.
func AttrPutString(kvl *KeyValueList, key, v string) { AttrPut(kvl, key, Str(v)) }

// AttrGet returns the value for an exact key match.
func AttrGet(kvl *KeyValueList, key string) (*Value, bool) {
	for _, kv := range kvl.GetValues() {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return nil, false
}

// AttrGetFold resolves a key case-insensitively (spec §Key Case Sensitivity):
// an exact match wins, otherwise the first insertion-order fold match.
func AttrGetFold(kvl *KeyValueList, key string) (*Value, bool) {
	if v, ok := AttrGet(kvl, key); ok {
		return v, true
	}
	for _, kv := range kvl.GetValues() {
		if strings.EqualFold(kv.Key, key) {
			return kv.Value, true
		}
	}
	return nil, false
}

// AttrDelete removes a key if present.
func AttrDelete(kvl *KeyValueList, key string) {
	for i, kv := range kvl.Values {
		if kv.Key == key {
			kvl.Values = append(kvl.Values[:i], kvl.Values[i+1:]...)
			return
		}
	}
}

// Fingerprint returns a stable identity string for an attribute set, useful for
// grouping series (spec §Attributes: sets of key/value pairs identify telemetry).
func Fingerprint(kvl *KeyValueList) string {
	kvs := kvl.GetValues()
	keys := make([]string, 0, len(kvs))
	byKey := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		keys = append(keys, kv.Key)
		byKey[kv.Key] = ValueString(kv.Value)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(byKey[k])
	}
	return b.String()
}

// ValueString renders a Value's scalar payload as text (for fingerprints and
// simple serialization); non-scalar values render as their type name.
func ValueString(v *Value) string {
	switch v.GetValue().(type) {
	case *qdatav1.Value_StringValue:
		return v.GetStringValue()
	case *qdatav1.Value_JsonValue:
		return v.GetJsonValue()
	default:
		return v.String()
	}
}

// SetMetadata sets a processor-to-processor hint on a Query, allocating the map
// lazily.
func SetMetadata(q *Query, key, value string) {
	if q.Metadata == nil {
		q.Metadata = make(map[string]string)
	}
	q.Metadata[key] = value
}

// ---- Feedback side channel (spec §Side Channel Feedback) ----

// Notify appends a notification to a Result's feedback channel, allocating it
// lazily. A UI or API can surface these without failing the query.
func Notify(r *Result, sev qdatav1.NotificationSeverity, code, message, source string) {
	if r.Feedback == nil {
		r.Feedback = &Feedback{}
	}
	r.Feedback.Notifications = append(r.Feedback.Notifications, &Notification{
		Severity: sev,
		Code:     code,
		Message:  message,
		Source:   source,
	})
}

// Warn is a shortcut for a warning-severity notification.
func Warn(r *Result, code, message, source string) {
	Notify(r, NotifyWarning, code, message, source)
}
