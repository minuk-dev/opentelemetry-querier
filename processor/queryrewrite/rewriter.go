package queryrewrite

import "github.com/minuk-dev/opentelemetry-querier/qdata"

// DialectRewriter understands one query dialect well enough to weave enforced
// predicates into its text. It is the pluggable comprehension step: the
// query-rewrite processor stays dialect-neutral and delegates the actual
// parse-and-inject to the rewriter registered for the query's dialect.
// Implementing this for LogQL/SQL/Lucene is how understanding is added without
// changing the processor (see docs/design/qdata-cross-language-query.md, Phase 1).
type DialectRewriter interface {
	// Dialect is the query.dialect tag this rewriter handles, e.g. "promql".
	Dialect() string
	// Enforce weaves preds into expr and returns the rewritten text. preds are
	// attribute predicates (label name + match op, implicitly AND-ed); enforcement
	// must win over any user-supplied predicate on the same attribute. The predicate
	// shape is label/PromQL-oriented today; a richer language-neutral predicate
	// model (boolean composition, non-label fields) is Phase 2 of the design note
	// (docs/design/qdata-cross-language-query.md, #10).
	Enforce(expr string, preds []*qdata.LabelMatcher) (string, error)
}

// defaultRewriters returns the built-in rewriter registry, keyed by dialect. Only
// PromQL is understood today; other dialects fall through to pass-through. A fresh
// map is returned per call so WithRewriter can extend one Processor's set without
// affecting another's.
func defaultRewriters() map[string]DialectRewriter {
	return map[string]DialectRewriter{PromQLDialect: promqlRewriter{}}
}
