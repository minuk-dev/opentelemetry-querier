// Package queryrewrite implements the query-transformation processor. It weaves
// enforced label matchers (from the tenant processor and from static config)
// into the query expression, so a query cannot escape its tenant or scope. The
// processor itself is dialect-neutral: it collects language-neutral predicates
// and delegates the parse-and-inject to the DialectRewriter registered for the
// query's dialect (only PromQL today). This is the spec §4.1 "best-effort query
// proxy" step; see docs/design/qdata-cross-language-query.md for the design.
package queryrewrite

import (
	"context"
	"fmt"

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

// Processor rewrites queries by injecting enforced predicates via a per-dialect
// rewriter.
type Processor struct {
	processor.Base

	cfg       Config
	rewriters map[string]DialectRewriter
}

// New builds the query-rewrite processor seeded with the built-in dialect
// rewriters (PromQL).
func New(cfg Config) *Processor {
	return &Processor{Base: processor.Base{}, cfg: cfg, rewriters: defaultRewriters()}
}

// Register adds or replaces the rewriter for a dialect, letting a deployment
// teach the processor a new query language without changing this package.
func (p *Processor) Register(rewriter DialectRewriter) {
	p.rewriters[rewriter.Dialect()] = rewriter
}

// ProcessQuery injects enforced matchers into the query expression. It resolves
// the query's dialect (empty means PromQL) to a registered rewriter; dialects
// with no registered rewriter pass through untouched.
func (p *Processor) ProcessQuery(_ context.Context, query *qdata.Query) error {
	if query.GetExpr() == "" {
		return nil
	}

	dialect := query.GetDialect()
	if dialect == "" {
		dialect = PromQLDialect
	}

	rewriter, ok := p.rewriters[dialect]
	if !ok {
		return nil
	}

	preds := p.collectPredicates(query)
	if len(preds) == 0 {
		return nil
	}

	rewritten, err := rewriter.Enforce(query.GetExpr(), preds)
	if err != nil {
		return fmt.Errorf("queryrewrite: %s: %w", dialect, err)
	}

	query.Expr = rewritten
	qdata.SetMetadata(query, "queryrewrite.rewritten", "true")

	return nil
}

// collectPredicates merges the query's already-registered enforced matchers (e.g.
// from the tenant processor) with this processor's static config into a single
// language-neutral predicate list.
func (p *Processor) collectPredicates(query *qdata.Query) []*qdata.LabelMatcher {
	preds := append([]*qdata.LabelMatcher(nil), query.GetEnforcedMatchers()...)

	for _, label := range p.cfg.EnforceLabels {
		value := label.Value
		if label.FromTenant {
			value = qdata.TenantID(query)
		}

		if value == "" {
			continue
		}

		preds = append(preds, &qdata.LabelMatcher{
			Name:  label.Name,
			Op:    qdata.MatchEqual,
			Value: value,
		})
	}

	return preds
}
