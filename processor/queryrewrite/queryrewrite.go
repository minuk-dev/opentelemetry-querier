// Package queryrewrite implements the query-transformation processor. It weaves
// enforced label matchers (from the tenant processor and from static config)
// into the query expression, so a query cannot escape its tenant or scope. The
// processor itself is dialect-neutral: it collects the enforced predicates and
// delegates the parse-and-inject to the DialectRewriter registered for the
// query's dialect (only PromQL today). This is the spec §4.1 "best-effort query
// proxy" step; see docs/design/qdata-cross-language-query.md for the design.
package queryrewrite

import (
	"context"
	"fmt"

	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
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

// Option customizes a Processor at construction.
type Option func(*Processor)

// WithRewriter registers a dialect rewriter, letting a deployment teach the
// processor a new query language without changing this package. It applies at
// construction only, so the rewriter set is immutable once New returns and is
// therefore safe to read from concurrent ProcessQuery calls.
func WithRewriter(rewriter DialectRewriter) Option {
	return func(p *Processor) { p.rewriters[rewriter.Dialect()] = rewriter }
}

// Processor rewrites queries by injecting enforced predicates via a per-dialect
// rewriter. The rewriter set is fixed at construction (see WithRewriter).
type Processor struct {
	processor.Base

	cfg       Config
	rewriters map[string]DialectRewriter
}

// New builds the query-rewrite processor seeded with the built-in dialect
// rewriters (PromQL), plus any supplied via WithRewriter.
func New(cfg Config, opts ...Option) *Processor {
	proc := &Processor{Base: processor.Base{}, cfg: cfg, rewriters: defaultRewriters()}

	for _, opt := range opts {
		opt(proc)
	}

	return proc
}

// ProcessQuery injects the enforced matchers into the query expression. When
// there is nothing to enforce the query is left untouched. When there is, but no
// registered rewriter understands the query's dialect (empty means PromQL), it
// fails closed rather than forward an unenforced query that could escape its
// tenant or scope.
func (p *Processor) ProcessQuery(_ context.Context, query *qdata.Query) error {
	if query.GetExpr() == "" {
		return nil
	}

	preds := p.collectPredicates(query)
	if len(preds) == 0 {
		return nil
	}

	dialect := query.GetDialect()
	if dialect == "" {
		dialect = PromQLDialect
	}

	rewriter, ok := p.rewriters[dialect]
	if !ok {
		return qerror.New(qerror.CodeInternal,
			"queryrewrite: cannot enforce matchers on unsupported dialect %q", dialect)
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
// from the tenant processor) with this processor's static config. When there is
// no static config it returns the query's slice directly, since the rewriter only
// reads the predicates; the defensive copy is taken only when config is appended.
func (p *Processor) collectPredicates(query *qdata.Query) []*qdata.LabelMatcher {
	enforced := query.GetEnforcedMatchers()
	if len(p.cfg.EnforceLabels) == 0 {
		return enforced
	}

	preds := append([]*qdata.LabelMatcher(nil), enforced...)

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
