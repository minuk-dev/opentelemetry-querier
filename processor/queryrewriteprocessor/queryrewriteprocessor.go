// Package queryrewriteprocessor implements the query-transformation processor. It
// composes enforced predicates (from the tenant processor and from static config)
// into the structured query plan, so a query cannot escape its tenant or scope.
// Enforcement is language-neutral: the predicates are AND-ed into every Select
// node's filter of the qdata QueryPlan, and each dispatcher renders the result to
// its backend. There is no per-dialect text rewriting — the plan is the query
// (design note #10, Phase 3 / §4.2 enforcement).
package queryrewriteprocessor

import (
	"context"

	qdatav1 "github.com/minuk-dev/opentelemetry-querier/gen/qdata/v1"
	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// EnforceLabel is a statically-configured matcher to inject into every query.
type EnforceLabel struct {
	Name string `mapstructure:"name"`
	// Value is a static value; ignored when FromTenant is true.
	Value string `mapstructure:"value"`
	// FromTenant uses the resolved tenant id as the matcher value.
	FromTenant bool `mapstructure:"from_tenant"`
}

// Config configures static enforcement.
type Config struct {
	EnforceLabels []EnforceLabel `mapstructure:"enforce_labels"`
}

// Processor rewrites queries by composing enforced predicates into the plan.
type Processor struct {
	processor.Base

	cfg Config
}

// New builds the query-rewrite processor.
func New(cfg Config) *Processor {
	return &Processor{Base: processor.Base{}, cfg: cfg}
}

// ProcessQuery AND-s the enforced predicates into every Select node of the
// query plan. When there is no plan, or nothing to enforce, the query is left
// untouched. Because enforcement composes into the predicate tree (which
// supports AND/OR/NOT natively), no dialect parsing is needed and boolean
// enforcement is expressible; a dispatcher whose backend cannot render a given
// predicate shape fails closed at render time.
func (p *Processor) ProcessQuery(_ context.Context, query *qdata.Query) error {
	plan := query.GetPlan()
	if plan == nil {
		return nil
	}

	enforcement := p.enforcement(query)
	if enforcement == nil {
		return nil
	}

	composeEnforcement(plan.GetRoot(), enforcement)
	qdata.SetMetadata(query, "queryrewrite.rewritten", "true")

	return nil
}

// enforcement builds the single predicate to AND into every Select: the enforced
// matchers (from the tenant processor and this processor's static config) plus
// any enforced_predicates trees. Returns nil when there is nothing to enforce.
func (p *Processor) enforcement(query *qdata.Query) *qdata.Predicate {
	matchers := p.collectMatchers(query)
	trees := query.GetEnforcedPredicates()

	operands := make([]*qdata.Predicate, 0, len(matchers)+len(trees))
	for _, matcher := range matchers {
		operands = append(operands, qdata.LeafPredicate(matcher))
	}

	operands = append(operands, trees...)

	switch len(operands) {
	case 0:
		return nil
	case 1:
		return operands[0]
	default:
		return qdata.BoolPredicate(qdata.BoolAnd, operands...)
	}
}

// collectMatchers merges the query's already-registered enforced matchers (e.g.
// from the tenant processor) with this processor's static config, resolving
// from_tenant values and skipping empties.
func (p *Processor) collectMatchers(query *qdata.Query) []*qdata.LabelMatcher {
	enforced := query.GetEnforcedMatchers()
	if len(p.cfg.EnforceLabels) == 0 {
		return enforced
	}

	matchers := append([]*qdata.LabelMatcher(nil), enforced...)

	for _, label := range p.cfg.EnforceLabels {
		value := label.Value
		if label.FromTenant {
			value = qdata.TenantID(query)
		}

		if value == "" {
			continue
		}

		matchers = append(matchers, &qdata.LabelMatcher{Name: label.Name, Op: qdata.MatchEqual, Value: value})
	}

	return matchers
}

// composeEnforcement AND-s enforcement into every Select leaf of the plan tree.
func composeEnforcement(node *qdata.Node, enforcement *qdata.Predicate) {
	if node == nil {
		return
	}

	switch variant := node.GetOp().(type) {
	case *qdatav1.Node_Select:
		variant.Select.Filter = conjoin(variant.Select.GetFilter(), enforcement)
	case *qdatav1.Node_TimeAgg:
		composeEnforcement(variant.TimeAgg.GetInput(), enforcement)
	case *qdatav1.Node_Aggregate:
		composeEnforcement(variant.Aggregate.GetInput(), enforcement)
	case *qdatav1.Node_Function:
		for _, arg := range variant.Function.GetArgs() {
			composeEnforcement(arg, enforcement)
		}
	case *qdatav1.Node_Binary:
		composeEnforcement(variant.Binary.GetLhs(), enforcement)
		composeEnforcement(variant.Binary.GetRhs(), enforcement)
	}
}

// conjoin returns the AND of an existing Select filter and the enforcement
// predicate, flattening top-level ANDs so a pure conjunction stays flat (which
// keeps label-selector dispatchers able to render it).
func conjoin(existing, enforcement *qdata.Predicate) *qdata.Predicate {
	if existing == nil {
		return enforcement
	}

	operands := append(andOperands(existing), andOperands(enforcement)...)

	return qdata.BoolPredicate(qdata.BoolAnd, operands...)
}

// andOperands returns a predicate's operands when it is a top-level AND, else the
// predicate itself as a single-element slice.
func andOperands(pred *qdata.Predicate) []*qdata.Predicate {
	if expr := pred.GetBoolExpr(); expr != nil && expr.GetOp() == qdata.BoolAnd {
		return expr.GetOperands()
	}

	return []*qdata.Predicate{pred}
}
