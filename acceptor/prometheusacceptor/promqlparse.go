package prometheusacceptor

import (
	"errors"
	"fmt"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

var (
	errUnsupportedExpr  = errors.New("prometheusacceptor: unsupported PromQL expression")
	errRangeSelector    = errors.New("prometheusacceptor: range function expects a range selector")
	errUnknownMatchType = errors.New("prometheusacceptor: unknown match type")
	errUnsupportedAgg   = errors.New("prometheusacceptor: unsupported aggregation")
	errUnsupportedBinOp = errors.New("prometheusacceptor: unsupported binary operator")
)

// parsePromQL parses PromQL text into a structured qdata QueryPlan. Unsupported
// constructs (subqueries, range functions outside the mapped set, unknown
// operators) return an error so the acceptor can reject them with 400 rather
// than produce a plan a dispatcher would mis-render.
func parsePromQL(text string) (*qdata.QueryPlan, error) {
	promQLParser := parser.NewParser(parser.Options{
		EnableExperimentalFunctions:  false,
		ExperimentalDurationExpr:     false,
		EnableExtendedRangeSelectors: false,
		EnableBinopFillModifiers:     false,
	})

	expr, err := promQLParser.ParseExpr(text)
	if err != nil {
		return nil, fmt.Errorf("prometheusacceptor: parse promql: %w", err)
	}

	node, err := convertExpr(expr)
	if err != nil {
		return nil, err
	}

	return qdata.Plan(node), nil
}

func convertExpr(expr parser.Expr) (*qdata.Node, error) {
	switch typed := expr.(type) {
	case *parser.ParenExpr:
		return convertExpr(typed.Expr)
	case *parser.StepInvariantExpr:
		return convertExpr(typed.Expr)
	case *parser.VectorSelector:
		return convertVectorSelector(typed)
	case *parser.NumberLiteral:
		return qdata.LiteralNode(typed.Val), nil
	case *parser.Call:
		return convertCall(typed)
	case *parser.AggregateExpr:
		return convertAggregate(typed)
	case *parser.BinaryExpr:
		return convertBinary(typed)
	case *parser.UnaryExpr:
		return convertUnary(typed)
	default:
		return nil, fmt.Errorf("%w: %T", errUnsupportedExpr, expr)
	}
}

// convertVectorSelector builds a metrics Select from a selector's label matchers
// (which already include the __name__ matcher when a metric name is given).
func convertVectorSelector(sel *parser.VectorSelector) (*qdata.Node, error) {
	filter, err := matchersToPredicate(sel.LabelMatchers)
	if err != nil {
		return nil, err
	}

	return qdata.SelectNode(qdata.SignalMetrics, filter), nil
}

func matchersToPredicate(matchers []*labels.Matcher) (*qdata.Predicate, error) {
	leaves := make([]*qdata.Predicate, 0, len(matchers))

	for _, matcher := range matchers {
		operator, err := convertMatchType(matcher.Type)
		if err != nil {
			return nil, err
		}

		leaves = append(leaves, qdata.LeafPredicate(
			&qdata.LabelMatcher{Name: matcher.Name, Op: operator, Value: matcher.Value}))
	}

	if len(leaves) == 1 {
		return leaves[0], nil
	}

	return qdata.BoolPredicate(qdata.BoolAnd, leaves...), nil
}

// convertCall maps a range-vector function (rate/increase/*_over_time) to a
// TimeAgg over its matrix-selector argument, and any other function to a
// Function node.
func convertCall(call *parser.Call) (*qdata.Node, error) {
	if operator, ok := rangeFunc(call.Func.Name); ok {
		matrix, ok := singleMatrixArg(call.Args)
		if !ok {
			return nil, fmt.Errorf("%w: %s", errRangeSelector, call.Func.Name)
		}

		selector, ok := matrix.VectorSelector.(*parser.VectorSelector)
		if !ok {
			return nil, fmt.Errorf("%w: %s", errRangeSelector, call.Func.Name)
		}

		input, err := convertVectorSelector(selector)
		if err != nil {
			return nil, err
		}

		return qdata.TimeAggNode(operator, matrix.Range, input), nil
	}

	args := make([]*qdata.Node, 0, len(call.Args))

	var stringArgs []string

	for _, arg := range call.Args {
		if str, ok := arg.(*parser.StringLiteral); ok {
			stringArgs = append(stringArgs, str.Val)

			continue
		}

		node, err := convertExpr(arg)
		if err != nil {
			return nil, err
		}

		args = append(args, node)
	}

	return qdata.FunctionNode(call.Func.Name, args, stringArgs...), nil
}

func singleMatrixArg(args parser.Expressions) (*parser.MatrixSelector, bool) {
	if len(args) != 1 {
		return nil, false
	}

	matrix, ok := args[0].(*parser.MatrixSelector)

	return matrix, ok
}

func convertAggregate(agg *parser.AggregateExpr) (*qdata.Node, error) {
	operator, err := convertAggOp(agg.Op)
	if err != nil {
		return nil, err
	}

	input, err := convertExpr(agg.Expr)
	if err != nil {
		return nil, err
	}

	var byLabels, withoutLabels []string
	if agg.Without {
		withoutLabels = agg.Grouping
	} else {
		byLabels = agg.Grouping
	}

	var param float64
	if number, ok := agg.Param.(*parser.NumberLiteral); ok {
		param = number.Val
	}

	return qdata.AggregateNode(operator, byLabels, withoutLabels, param, input), nil
}

func convertBinary(bin *parser.BinaryExpr) (*qdata.Node, error) {
	operator, err := convertBinOp(bin.Op)
	if err != nil {
		return nil, err
	}

	lhs, err := convertExpr(bin.LHS)
	if err != nil {
		return nil, err
	}

	rhs, err := convertExpr(bin.RHS)
	if err != nil {
		return nil, err
	}

	return qdata.BinaryNode(operator, lhs, rhs, convertMatching(bin.VectorMatching)), nil
}

// convertUnary maps a unary minus to 0 - x; a unary plus is a no-op.
func convertUnary(unary *parser.UnaryExpr) (*qdata.Node, error) {
	inner, err := convertExpr(unary.Expr)
	if err != nil {
		return nil, err
	}

	if unary.Op == parser.SUB {
		return qdata.BinaryNode(qdata.BinSub, qdata.LiteralNode(0), inner, nil), nil
	}

	return inner, nil
}

func convertMatching(matching *parser.VectorMatching) *qdata.VectorMatch {
	if matching == nil {
		return nil
	}

	match := &qdata.VectorMatch{Include: matching.Include}

	if matching.On {
		match.On = matching.MatchingLabels
	} else {
		match.Ignoring = matching.MatchingLabels
	}

	switch matching.Card {
	case parser.CardManyToOne:
		match.Cardinality = qdata.CardinalityManyToOne
	case parser.CardOneToMany:
		match.Cardinality = qdata.CardinalityOneToMany
	case parser.CardOneToOne, parser.CardManyToMany:
		match.Cardinality = qdata.CardinalityOneToOne
	}

	return match
}

func convertMatchType(matchType labels.MatchType) (qdata.MatchOp, error) {
	switch matchType {
	case labels.MatchEqual:
		return qdata.MatchEqual, nil
	case labels.MatchNotEqual:
		return qdata.MatchNotEqual, nil
	case labels.MatchRegexp:
		return qdata.MatchRegexp, nil
	case labels.MatchNotRegexp:
		return qdata.MatchNotRegexp, nil
	default:
		return qdata.MatchEqual, fmt.Errorf("%w: %v", errUnknownMatchType, matchType)
	}
}

// rangeFunc maps a PromQL range-vector function name to a TimeAggOp.
func rangeFunc(name string) (qdata.TimeAggOp, bool) {
	operator, ok := map[string]qdata.TimeAggOp{
		"rate":            qdata.TimeAggRate,
		"increase":        qdata.TimeAggIncrease,
		"count_over_time": qdata.TimeAggCountOverTime,
		"sum_over_time":   qdata.TimeAggSumOverTime,
		"avg_over_time":   qdata.TimeAggAvgOverTime,
		"min_over_time":   qdata.TimeAggMinOverTime,
		"max_over_time":   qdata.TimeAggMaxOverTime,
	}[name]

	return operator, ok
}

func convertAggOp(item parser.ItemType) (qdata.AggOp, error) {
	operator, ok := map[parser.ItemType]qdata.AggOp{
		parser.SUM:      qdata.AggSum,
		parser.AVG:      qdata.AggAvg,
		parser.MIN:      qdata.AggMin,
		parser.MAX:      qdata.AggMax,
		parser.COUNT:    qdata.AggCount,
		parser.QUANTILE: qdata.AggQuantile,
		parser.TOPK:     qdata.AggTopK,
		parser.BOTTOMK:  qdata.AggBottomK,
		parser.GROUP:    qdata.AggGroup,
		parser.STDDEV:   qdata.AggStddev,
		parser.STDVAR:   qdata.AggStdvar,
	}[item]
	if !ok {
		return operator, fmt.Errorf("%w: %s", errUnsupportedAgg, item)
	}

	return operator, nil
}

func convertBinOp(item parser.ItemType) (qdata.BinOp, error) {
	operator, ok := map[parser.ItemType]qdata.BinOp{
		parser.ADD:     qdata.BinAdd,
		parser.SUB:     qdata.BinSub,
		parser.MUL:     qdata.BinMul,
		parser.DIV:     qdata.BinDiv,
		parser.MOD:     qdata.BinMod,
		parser.POW:     qdata.BinPow,
		parser.EQLC:    qdata.BinEq,
		parser.NEQ:     qdata.BinNe,
		parser.GTR:     qdata.BinGt,
		parser.LSS:     qdata.BinLt,
		parser.GTE:     qdata.BinGe,
		parser.LTE:     qdata.BinLe,
		parser.LAND:    qdata.BinAnd,
		parser.LOR:     qdata.BinOr,
		parser.LUNLESS: qdata.BinUnless,
	}[item]
	if !ok {
		return operator, fmt.Errorf("%w: %s", errUnsupportedBinOp, item)
	}

	return operator, nil
}
