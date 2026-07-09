// Package queryrewrite implements the query-transformation processor. It weaves
// enforced label matchers (from the tenant processor and from static config)
// into the PromQL expression's AST, matching the prom-label-proxy technique:
// every vector/matrix selector gains the enforced matchers, so a query cannot
// escape its tenant or scope. This is the spec §4.1 "best-effort query proxy"
// transpilation step for the PromQL dialect.
package queryrewrite

import (
	"context"
	"errors"
	"fmt"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// errUnknownMatchOp is returned when a matcher uses an unrecognized operator.
var errUnknownMatchOp = errors.New("queryrewrite: unknown match op")

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

// Processor rewrites PromQL queries.
type Processor struct {
	processor.Base

	cfg Config
}

// New builds the query-rewrite processor.
func New(cfg Config) *Processor { return &Processor{Base: processor.Base{}, cfg: cfg} }

// ProcessQuery injects enforced matchers into the query expression. It only
// touches the PromQL dialect; other dialects pass through untouched (a real
// deployment would register a rewriter per dialect).
func (p *Processor) ProcessQuery(_ context.Context, query *qdata.Query) error {
	if query.GetExpr() == "" {
		return nil
	}

	if dialect := query.GetDialect(); dialect != "" && dialect != "promql" {
		return nil
	}

	matchers, err := p.collectMatchers(query)
	if err != nil {
		return err
	}

	if len(matchers) == 0 {
		return nil
	}

	rewritten, err := enforce(query.GetExpr(), matchers)
	if err != nil {
		return err
	}

	query.Expr = rewritten
	qdata.SetMetadata(query, "queryrewrite.rewritten", "true")

	return nil
}

// collectMatchers merges the query's already-registered enforced matchers (e.g.
// from the tenant processor) with this processor's static config.
func (p *Processor) collectMatchers(query *qdata.Query) ([]*labels.Matcher, error) {
	var out []*labels.Matcher

	for _, enforced := range query.GetEnforcedMatchers() {
		matcher, err := toLabelsMatcher(enforced)
		if err != nil {
			return nil, err
		}

		out = append(out, matcher)
	}

	for _, label := range p.cfg.EnforceLabels {
		value := label.Value
		if label.FromTenant {
			value = query.GetTenantId()
		}

		if value == "" {
			continue
		}

		matcher, err := labels.NewMatcher(labels.MatchEqual, label.Name, value)
		if err != nil {
			return nil, fmt.Errorf("queryrewrite: new matcher: %w", err)
		}

		out = append(out, matcher)
	}

	return out, nil
}

// enforce parses expr, injects matchers into every selector, and re-renders it.
func enforce(expr string, matchers []*labels.Matcher) (string, error) {
	// Prometheus 3.x replaced the package-level ParseExpr with a Parser instance.
	// A fresh parser per call keeps enforce safe for concurrent queries.
	promQLParser := parser.NewParser(parser.Options{
		EnableExperimentalFunctions:  false,
		ExperimentalDurationExpr:     false,
		EnableExtendedRangeSelectors: false,
		EnableBinopFillModifiers:     false,
	})

	astExpr, err := promQLParser.ParseExpr(expr)
	if err != nil {
		return "", fmt.Errorf("queryrewrite: parse: %w", err)
	}

	parser.Inspect(astExpr, func(node parser.Node, _ []parser.Node) error {
		if selector, ok := node.(*parser.VectorSelector); ok {
			selector.LabelMatchers = mergeMatchers(selector.LabelMatchers, matchers)
		}

		return nil
	})

	return astExpr.String(), nil
}

// mergeMatchers appends the enforced matchers, dropping any existing matcher on
// the same label so enforcement always wins.
func mergeMatchers(existing, enforced []*labels.Matcher) []*labels.Matcher {
	enforcedNames := make(map[string]struct{}, len(enforced))
	for _, matcher := range enforced {
		enforcedNames[matcher.Name] = struct{}{}
	}

	out := existing[:0:0]

	for _, matcher := range existing {
		if _, clash := enforcedNames[matcher.Name]; !clash {
			out = append(out, matcher)
		}
	}

	return append(out, enforced...)
}

func toLabelsMatcher(matcher *qdata.LabelMatcher) (*labels.Matcher, error) {
	var matchType labels.MatchType

	switch matcher.GetOp() {
	case qdata.MatchEqual:
		matchType = labels.MatchEqual
	case qdata.MatchNotEqual:
		matchType = labels.MatchNotEqual
	case qdata.MatchRegexp:
		matchType = labels.MatchRegexp
	case qdata.MatchNotRegexp:
		matchType = labels.MatchNotRegexp
	default:
		return nil, fmt.Errorf("%w %v", errUnknownMatchOp, matcher.GetOp())
	}

	built, err := labels.NewMatcher(matchType, matcher.GetName(), matcher.GetValue())
	if err != nil {
		return nil, fmt.Errorf("queryrewrite: new matcher: %w", err)
	}

	return built, nil
}
