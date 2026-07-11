// Package qdata provides ergonomic aliases and helpers over the generated
// gen/qdata/v1 protobuf types, which are the standardized query & result model
// flowing through the querier pipeline (the equivalent of opentelemetry-collector
// pdata). Generated messages cannot carry hand-written methods from this package,
// so the helpers here are free functions.
package qdata

import (
	"errors"
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
	// Query is the standardized, DSL-agnostic query request.
	Query = qdatav1.Query
	// Result is the standardized query response.
	Result = qdatav1.Result
	// TimeRange is a query time window.
	TimeRange = qdatav1.TimeRange
	// Modifier captures out-of-range fetch adjustments.
	Modifier = qdatav1.Modifier
	// LabelMatcher is a single attribute predicate to enforce on a query.
	LabelMatcher = qdatav1.LabelMatcher
	// Predicate is one node of an enforcement predicate tree (leaf or bool expr).
	Predicate = qdatav1.Predicate
	// BoolExpr is a boolean combination of child Predicates.
	BoolExpr = qdatav1.BoolExpr
	// BoolOp is the operator combining a BoolExpr's operands.
	BoolOp = qdatav1.BoolOp
	// HeaderValues wraps the repeated values of one header.
	HeaderValues = qdatav1.HeaderValues

	// Value is a tagged union over the QLSWG data types.
	Value = qdatav1.Value
	// ArrayValue is an ordered list of values.
	ArrayValue = qdatav1.ArrayValue
	// KeyValue is one attribute entry.
	KeyValue = qdatav1.KeyValue
	// KeyValueList is the flattened attribute map.
	KeyValueList = qdatav1.KeyValueList

	// Metrics is a collection of metric series.
	Metrics = qdatav1.Metrics
	// MetricSeries is a set of measurements sharing an identity.
	MetricSeries = qdatav1.MetricSeries
	// MetricPoint is a single (windowed) measurement.
	MetricPoint = qdatav1.MetricPoint
	// Exemplar is a sample of a raw measurement.
	Exemplar = qdatav1.Exemplar
	// Logs is a collection of log records.
	Logs = qdatav1.Logs
	// LogRecord unifies logs, events and wide events.
	LogRecord = qdatav1.LogRecord
	// Spans is a collection of spans.
	Spans = qdatav1.Spans
	// Span keeps the OTel span fields as columns.
	Span = qdatav1.Span

	// Feedback is the side channel carried with a Result.
	Feedback = qdatav1.Feedback
	// Notification is one side-channel message explaining a result.
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

	BoolAnd = qdatav1.BoolOp_BOOL_OP_AND
	BoolOr  = qdatav1.BoolOp_BOOL_OP_OR
	BoolNot = qdatav1.BoolOp_BOOL_OP_NOT

	MetricGauge             = qdatav1.MetricType_METRIC_TYPE_GAUGE
	MetricCumulativeCounter = qdatav1.MetricType_METRIC_TYPE_CUMULATIVE_COUNTER
	MetricDeltaCounter      = qdatav1.MetricType_METRIC_TYPE_DELTA_COUNTER
	MetricUnknown           = qdatav1.MetricType_METRIC_TYPE_UNSPECIFIED

	NotifyInfo    = qdatav1.NotificationSeverity_NOTIFICATION_SEVERITY_INFO
	NotifyWarning = qdatav1.NotificationSeverity_NOTIFICATION_SEVERITY_WARNING
	NotifyError   = qdatav1.NotificationSeverity_NOTIFICATION_SEVERITY_ERROR
)

// ---- Value constructors (spec §Data Types) ----

// Double builds a double-typed Value.
func Double(v float64) *Value { return &Value{Value: &qdatav1.Value_DoubleValue{DoubleValue: v}} }

// Int builds a signed-integer Value.
func Int(v int64) *Value { return &Value{Value: &qdatav1.Value_IntValue{IntValue: v}} }

// Uint builds an unsigned-integer Value.
func Uint(v uint64) *Value { return &Value{Value: &qdatav1.Value_UintValue{UintValue: v}} }

// Str builds a string Value.
func Str(v string) *Value { return &Value{Value: &qdatav1.Value_StringValue{StringValue: v}} }

// Bool builds a boolean Value.
func Bool(v bool) *Value { return &Value{Value: &qdatav1.Value_BoolValue{BoolValue: v}} }

// JSON builds a JSON-typed Value from raw JSON text.
func JSON(raw string) *Value { return &Value{Value: &qdatav1.Value_JsonValue{JsonValue: raw}} }

// Timestamp builds a timestamp Value.
func Timestamp(t time.Time) *Value {
	return &Value{Value: &qdatav1.Value_TimestampValue{TimestampValue: timestamppb.New(t)}}
}

// DurationVal builds a duration Value.
func DurationVal(d time.Duration) *Value {
	return &Value{Value: &qdatav1.Value_DurationValue{DurationValue: durationpb.New(d)}}
}

// Array builds an array Value.
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
func AttrPut(kvl *KeyValueList, key string, value *Value) {
	for _, kv := range kvl.GetValues() {
		if kv.GetKey() == key {
			kv.Value = value

			return
		}
	}

	kvl.Values = append(kvl.Values, &KeyValue{Key: key, Value: value})
}

// AttrPutString is a shortcut for a string-valued attribute.
func AttrPutString(kvl *KeyValueList, key, value string) { AttrPut(kvl, key, Str(value)) }

// AttrGet returns the value for an exact key match.
func AttrGet(kvl *KeyValueList, key string) (*Value, bool) {
	for _, kv := range kvl.GetValues() {
		if kv.GetKey() == key {
			return kv.GetValue(), true
		}
	}

	return nil, false
}

// AttrGetFold resolves a key case-insensitively (spec §Key Case Sensitivity):
// an exact match wins, otherwise the first insertion-order fold match.
func AttrGetFold(kvl *KeyValueList, key string) (*Value, bool) {
	if value, ok := AttrGet(kvl, key); ok {
		return value, true
	}

	for _, kv := range kvl.GetValues() {
		if strings.EqualFold(kv.GetKey(), key) {
			return kv.GetValue(), true
		}
	}

	return nil, false
}

// AttrDelete removes a key if present.
func AttrDelete(kvl *KeyValueList, key string) {
	for i, kv := range kvl.GetValues() {
		if kv.GetKey() == key {
			kvl.Values = append(kvl.Values[:i], kvl.Values[i+1:]...)

			return
		}
	}
}

// Fingerprint returns a stable identity string for an attribute set, useful for
// grouping series (spec §Attributes: sets of key/value pairs identify telemetry).
func Fingerprint(kvl *KeyValueList) string {
	entries := kvl.GetValues()
	keys := make([]string, 0, len(entries))
	byKey := make(map[string]string, len(entries))

	for _, kv := range entries {
		keys = append(keys, kv.GetKey())
		byKey[kv.GetKey()] = ValueString(kv.GetValue())
	}

	sort.Strings(keys)

	var builder strings.Builder

	for i, key := range keys {
		if i > 0 {
			builder.WriteByte(',')
		}

		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(byKey[key])
	}

	return builder.String()
}

// ValueString renders a Value's scalar payload as text (for fingerprints and
// simple serialization); non-scalar values render as their type name.
func ValueString(value *Value) string {
	switch value.GetValue().(type) {
	case *qdatav1.Value_StringValue:
		return value.GetStringValue()
	case *qdatav1.Value_JsonValue:
		return value.GetJsonValue()
	default:
		return value.String()
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

// Metadata reads a processor-to-processor hint from a Query, returning the empty
// string when absent.
func Metadata(q *Query, key string) string { return q.GetMetadata()[key] }

// MetadataTenantID is the metadata key holding the resolved tenant id. Tenancy
// is request-context/transport metadata (Cortex/Mimir/Thanos/Loki X-Scope-OrgID)
// rather than a query-language field, so it lives here instead of on Query.
const MetadataTenantID = "tenant.id"

// TenantID returns the resolved tenant id, or the empty string when unresolved.
func TenantID(q *Query) string { return Metadata(q, MetadataTenantID) }

// SetTenantID records the resolved tenant id in the Query's metadata.
func SetTenantID(q *Query, id string) { SetMetadata(q, MetadataTenantID, id) }

// Canonical dialect tags name the query language carried in Query.expr — the
// "DSL text transport" layer of the QLSWG spec (§4.1, best-effort proxy). These
// are the registered values for Query.dialect; an empty dialect means the
// default, DialectPromQL.
//
// The dialect contract (design note #10, Phase 0): components agree on who may
// touch expr for a given dialect.
//   - A dispatcher must reject (or pass through unchanged) a dialect it can't
//     execute against its backend — never mis-send another language's text.
//   - A processor must no-op, or fail closed, on a dialect it can't parse rather
//     than silently forward an unenforced query.
const (
	DialectPromQL = "promql"
	DialectLogQL  = "logql"
	DialectLucene = "lucene"
	DialectSQL    = "sql"
)

// QueryDialect returns q's dialect, resolving the empty default to DialectPromQL
// so callers don't each special-case the empty string.
func QueryDialect(q *Query) string {
	if d := q.GetDialect(); d != "" {
		return d
	}

	return DialectPromQL
}

// KnownDialect reports whether dialect is one of the canonical registered tags.
// It does not imply any given component can execute it — only that the tag is
// part of the recognized vocabulary.
func KnownDialect(dialect string) bool {
	switch dialect {
	case DialectPromQL, DialectLogQL, DialectLucene, DialectSQL:
		return true
	default:
		return false
	}
}

// ---- Enforcement predicate trees (design note #10, Phase 2) ----

// Predicate-tree errors. These report structural (shape) problems, not dialect
// support: a well-formed tree may still be unenforceable by a given injector.
var (
	errNilPredicate   = errors.New("qdata: nil predicate")
	errEmptyPredicate = errors.New("qdata: predicate has no leaf or bool node set")
	errNilLeaf        = errors.New("qdata: predicate leaf is nil")
	errNotArity       = errors.New("qdata: NOT predicate must have exactly one operand")
	errBoolNoOperands = errors.New("qdata: AND/OR predicate needs at least one operand")
	errUnknownBoolOp  = errors.New("qdata: unknown bool op")
)

// LeafPredicate wraps a LabelMatcher as a predicate-tree leaf.
func LeafPredicate(matcher *LabelMatcher) *Predicate {
	return &Predicate{Node: &qdatav1.Predicate_Leaf{Leaf: matcher}}
}

// BoolPredicate builds a boolean predicate node combining operands with op.
func BoolPredicate(op BoolOp, operands ...*Predicate) *Predicate {
	return &Predicate{Node: &qdatav1.Predicate_BoolExpr{BoolExpr: &BoolExpr{Op: op, Operands: operands}}}
}

// ValidatePredicate reports whether p is a structurally well-formed predicate
// tree: every node sets a leaf or a bool expr, a leaf is non-nil, NOT has exactly
// one operand, AND/OR have at least one, and every descendant is valid too. It
// checks shape only, not whether a dialect can enforce it.
func ValidatePredicate(p *Predicate) error {
	if p == nil {
		return errNilPredicate
	}

	switch node := p.GetNode().(type) {
	case *qdatav1.Predicate_Leaf:
		if node.Leaf == nil {
			return errNilLeaf
		}

		return nil
	case *qdatav1.Predicate_BoolExpr:
		return validateBoolExpr(node.BoolExpr)
	default:
		return errEmptyPredicate
	}
}

func validateBoolExpr(expr *BoolExpr) error {
	if expr == nil {
		return errEmptyPredicate
	}

	switch expr.GetOp() {
	case BoolNot:
		if len(expr.GetOperands()) != 1 {
			return errNotArity
		}
	case BoolAnd, BoolOr:
		if len(expr.GetOperands()) == 0 {
			return errBoolNoOperands
		}
	default:
		return errUnknownBoolOp
	}

	for _, operand := range expr.GetOperands() {
		err := ValidatePredicate(operand)
		if err != nil {
			return err
		}
	}

	return nil
}

// FlattenConjunction returns the leaf matchers of preds when the whole forest is
// a pure conjunction of leaves — a top-level AND, no OR/NOT — so a label-oriented
// injector (e.g. PromQL) can consume the common isolation case. ok is false when
// any node needs real boolean composition, letting the caller fail closed rather
// than silently drop enforcement. The preds slice (a Query.enforced_predicates
// forest) is itself implicitly AND-ed; a nil/empty forest flattens to no matchers
// with ok true.
func FlattenConjunction(preds []*Predicate) ([]*LabelMatcher, bool) {
	var out []*LabelMatcher

	for _, pred := range preds {
		if pred == nil {
			return nil, false
		}

		switch node := pred.GetNode().(type) {
		case *qdatav1.Predicate_Leaf:
			if node.Leaf == nil {
				return nil, false
			}

			out = append(out, node.Leaf)
		case *qdatav1.Predicate_BoolExpr:
			if node.BoolExpr.GetOp() != BoolAnd {
				return nil, false
			}

			nested, ok := FlattenConjunction(node.BoolExpr.GetOperands())
			if !ok {
				return nil, false
			}

			out = append(out, nested...)
		default:
			return nil, false
		}
	}

	return out, true
}

// ---- Feedback side channel (spec §Side Channel Feedback) ----

// Notify appends a notification to a Result's feedback channel, allocating it
// lazily. A UI or API can surface these without failing the query.
func Notify(r *Result, sev qdatav1.NotificationSeverity, code, message, source string) {
	if r.Feedback == nil {
		r.Feedback = &Feedback{}
	}

	r.Feedback.Notifications = append(r.GetFeedback().GetNotifications(), &Notification{
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
