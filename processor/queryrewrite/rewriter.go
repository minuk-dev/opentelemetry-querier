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
	// language-neutral attribute predicates (implicitly AND-ed); enforcement must
	// win over any user-supplied predicate on the same attribute.
	Enforce(expr string, preds []*qdata.LabelMatcher) (string, error)
}

// defaultRewriters returns the built-in rewriter registry, keyed by dialect. Only
// PromQL is understood today; other dialects fall through to pass-through.
func defaultRewriters() map[string]DialectRewriter {
	promql := promqlRewriter{}

	return map[string]DialectRewriter{promql.Dialect(): promql}
}
