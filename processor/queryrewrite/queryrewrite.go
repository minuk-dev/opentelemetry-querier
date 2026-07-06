// Package queryrewrite implements the query-transformation processor. It weaves
// enforced label matchers (from the tenant processor and from static config)
// into the PromQL expression's AST, matching the prom-label-proxy technique:
// every vector/matrix selector gains the enforced matchers, so a query cannot
// escape its tenant or scope. This is the spec §4.1 "best-effort query proxy"
// transpilation step for the PromQL dialect.
package queryrewrite

import (
	"context"
	"fmt"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// EnforceLabel is a statically-configured matcher to inject into every query.
type EnforceLabel struct {
	Name string `yaml:"name"`
	// Value is a static value; ignored when FromTenant is true.
	Value string `yaml:"value"`
	// FromTenant uses the resolved tenant id as the matcher value.
	FromTenant bool `yaml:"from_tenant"`
}

// Config configures static enforcement.
type Config struct {
	EnforceLabels []EnforceLabel `yaml:"enforce_labels"`
}

// Processor rewrites PromQL queries.
type Processor struct {
	processor.Base
	cfg Config
}

// New builds the query-rewrite processor.
func New(cfg Config) *Processor { return &Processor{cfg: cfg} }

func (p *Processor) Name() string { return "queryrewrite" }

// ProcessQuery injects enforced matchers into the query expression. It only
// touches the PromQL dialect; other dialects pass through untouched (a real
// deployment would register a rewriter per dialect).
func (p *Processor) ProcessQuery(_ context.Context, q *qdata.Query) error {
	if q.GetExpr() == "" {
		return nil
	}
	if d := q.GetDialect(); d != "" && d != "promql" {
		return nil
	}

	matchers, err := p.collectMatchers(q)
	if err != nil {
		return err
	}
	if len(matchers) == 0 {
		return nil
	}

	rewritten, err := enforce(q.GetExpr(), matchers)
	if err != nil {
		return fmt.Errorf("queryrewrite: %w", err)
	}
	q.Expr = rewritten
	qdata.SetMetadata(q, "queryrewrite.rewritten", "true")
	return nil
}

// collectMatchers merges the query's already-registered enforced matchers (e.g.
// from the tenant processor) with this processor's static config.
func (p *Processor) collectMatchers(q *qdata.Query) ([]*labels.Matcher, error) {
	var out []*labels.Matcher

	for _, m := range q.GetEnforcedMatchers() {
		lm, err := toLabelsMatcher(m)
		if err != nil {
			return nil, err
		}
		out = append(out, lm)
	}

	for _, el := range p.cfg.EnforceLabels {
		value := el.Value
		if el.FromTenant {
			value = q.GetTenantId()
		}
		if value == "" {
			continue
		}
		lm, err := labels.NewMatcher(labels.MatchEqual, el.Name, value)
		if err != nil {
			return nil, err
		}
		out = append(out, lm)
	}
	return out, nil
}

// enforce parses expr, injects matchers into every selector, and re-renders it.
func enforce(expr string, matchers []*labels.Matcher) (string, error) {
	e, err := parser.ParseExpr(expr)
	if err != nil {
		return "", err
	}
	parser.Inspect(e, func(node parser.Node, _ []parser.Node) error {
		if vs, ok := node.(*parser.VectorSelector); ok {
			vs.LabelMatchers = mergeMatchers(vs.LabelMatchers, matchers)
		}
		return nil
	})
	return e.String(), nil
}

// mergeMatchers appends the enforced matchers, dropping any existing matcher on
// the same label so enforcement always wins.
func mergeMatchers(existing, enforced []*labels.Matcher) []*labels.Matcher {
	enforcedNames := make(map[string]struct{}, len(enforced))
	for _, m := range enforced {
		enforcedNames[m.Name] = struct{}{}
	}
	out := existing[:0:0]
	for _, m := range existing {
		if _, clash := enforcedNames[m.Name]; !clash {
			out = append(out, m)
		}
	}
	return append(out, enforced...)
}

func toLabelsMatcher(m *qdata.LabelMatcher) (*labels.Matcher, error) {
	var t labels.MatchType
	switch m.GetOp() {
	case qdata.MatchEqual:
		t = labels.MatchEqual
	case qdata.MatchNotEqual:
		t = labels.MatchNotEqual
	case qdata.MatchRegexp:
		t = labels.MatchRegexp
	case qdata.MatchNotRegexp:
		t = labels.MatchNotRegexp
	default:
		return nil, fmt.Errorf("queryrewrite: unknown match op %v", m.GetOp())
	}
	return labels.NewMatcher(t, m.GetName(), m.GetValue())
}
