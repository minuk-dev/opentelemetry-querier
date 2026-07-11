package queryrewrite

import (
	"errors"
	"fmt"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// PromQLDialect is the dialect tag for PromQL, and the assumed default when a
// query carries no explicit dialect.
const PromQLDialect = "promql"

// errUnknownMatchOp is returned when a matcher uses an unrecognized operator.
// Error strings here carry no package prefix; ProcessQuery adds the single
// "queryrewrite: <dialect>:" prefix at the package boundary.
var errUnknownMatchOp = errors.New("unknown match op")

// promqlRewriter injects enforced matchers into a PromQL expression's AST,
// matching the prom-label-proxy technique: every vector/matrix selector gains the
// enforced matchers, so a query cannot escape its tenant or scope. This is the
// spec §4.1 "best-effort query proxy" transpilation step for the PromQL dialect.
type promqlRewriter struct{}

// Dialect reports the dialect tag this rewriter handles.
func (promqlRewriter) Dialect() string { return PromQLDialect }

// Enforce parses expr, injects the neutral predicates into every selector, and
// re-renders the expression.
func (promqlRewriter) Enforce(expr string, preds []*qdata.LabelMatcher) (string, error) {
	matchers := make([]*labels.Matcher, 0, len(preds))

	for _, pred := range preds {
		matcher, err := toLabelsMatcher(pred)
		if err != nil {
			return "", err
		}

		matchers = append(matchers, matcher)
	}

	// Prometheus 3.x replaced the package-level ParseExpr with a Parser instance.
	// A fresh parser per call keeps Enforce safe for concurrent queries.
	promQLParser := parser.NewParser(parser.Options{
		EnableExperimentalFunctions:  false,
		ExperimentalDurationExpr:     false,
		EnableExtendedRangeSelectors: false,
		EnableBinopFillModifiers:     false,
	})

	astExpr, err := promQLParser.ParseExpr(expr)
	if err != nil {
		return "", fmt.Errorf("parse: %w", err)
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
		return nil, fmt.Errorf("new matcher: %w", err)
	}

	return built, nil
}
