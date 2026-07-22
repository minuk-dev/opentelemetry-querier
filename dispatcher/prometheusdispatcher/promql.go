package prometheusdispatcher

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	qdatav1 "github.com/minuk-dev/opentelemetry-querier/gen/qdata/v1"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// planToPromQL renders a structured qdata QueryPlan to a PromQL string. It fails
// closed (CodeInvalidArgument) on any node PromQL cannot express — a non-metrics
// Select, a selector needing OR/NOT, log line filters, or an unknown operator —
// so an unrenderable plan is rejected rather than mis-shipped.
func planToPromQL(plan *qdata.QueryPlan) (string, error) {
	return renderNode(plan.GetRoot())
}

func renderNode(node *qdata.Node) (string, error) {
	switch variant := node.GetOp().(type) {
	case *qdatav1.Node_Select:
		return renderSelect(variant.Select)
	case *qdatav1.Node_TimeAgg:
		return renderTimeAgg(variant.TimeAgg)
	case *qdatav1.Node_Aggregate:
		return renderAggregate(variant.Aggregate)
	case *qdatav1.Node_Function:
		return renderFunction(variant.Function)
	case *qdatav1.Node_Binary:
		return renderBinary(variant.Binary)
	case *qdatav1.Node_Literal:
		return strconv.FormatFloat(variant.Literal.GetValue(), 'f', fullPrecision, floatBitSize), nil
	default:
		return "", qerror.New(qerror.CodeInvalidArgument, "promdispatcher: empty plan node")
	}
}

// renderSelect renders an instant-vector selector. PromQL selectors are a flat
// conjunction of label matchers, so a filter that needs OR/NOT cannot render.
func renderSelect(sel *qdata.Select) (string, error) {
	if sel.GetSignal() != qdata.SignalMetrics {
		return "", qerror.New(qerror.CodeInvalidArgument,
			"promdispatcher: Prometheus serves metrics, not %s", sel.GetSignal())
	}

	if len(sel.GetLine()) > 0 {
		return "", qerror.New(qerror.CodeInvalidArgument, "promdispatcher: PromQL has no line filters")
	}

	matchers, ok := qdata.FlattenConjunction([]*qdata.Predicate{sel.GetFilter()})
	if !ok {
		return "", qerror.New(qerror.CodeInvalidArgument,
			"promdispatcher: a PromQL selector cannot express OR/NOT composition")
	}

	if len(matchers) == 0 {
		return "", qerror.New(qerror.CodeInvalidArgument,
			"promdispatcher: a PromQL selector needs at least one matcher")
	}

	parts := make([]string, 0, len(matchers))
	for _, matcher := range matchers {
		operator, opErr := matchOpSymbol(matcher.GetOp())
		if opErr != nil {
			return "", opErr
		}

		parts = append(parts, matcher.GetName()+operator+strconv.Quote(matcher.GetValue()))
	}

	sort.Strings(parts)

	return "{" + strings.Join(parts, ",") + "}", nil
}

func renderTimeAgg(agg *qdata.TimeAgg) (string, error) {
	funcName, ok := timeAggFunc(agg.GetOp())
	if !ok {
		return "", qerror.New(qerror.CodeInvalidArgument, "promdispatcher: unsupported range function")
	}

	input, err := renderNode(agg.GetInput())
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s(%s[%s])", funcName, input, promDuration(agg.GetWindow().AsDuration())), nil
}

func renderAggregate(agg *qdata.Aggregate) (string, error) {
	funcName, ok := aggFunc(agg.GetOp())
	if !ok {
		return "", qerror.New(qerror.CodeInvalidArgument, "promdispatcher: unsupported aggregation")
	}

	input, err := renderNode(agg.GetInput())
	if err != nil {
		return "", err
	}

	grouping := groupingClause(agg)

	// quantile/topk/bottomk take a leading scalar parameter.
	if aggTakesParam(agg.GetOp()) {
		param := strconv.FormatFloat(agg.GetParam(), 'f', fullPrecision, floatBitSize)

		return fmt.Sprintf("%s%s(%s, %s)", funcName, grouping, param, input), nil
	}

	return fmt.Sprintf("%s%s(%s)", funcName, grouping, input), nil
}

// groupingClause renders the optional " by(...)" / " without(...)" clause.
func groupingClause(agg *qdata.Aggregate) string {
	if by := agg.GetBy(); len(by) > 0 {
		return " by(" + strings.Join(by, ",") + ")"
	}

	if without := agg.GetWithout(); len(without) > 0 {
		return " without(" + strings.Join(without, ",") + ")"
	}

	return ""
}

func renderFunction(function *qdata.Function) (string, error) {
	args := make([]string, 0, len(function.GetArgs())+len(function.GetStringArgs()))

	for _, arg := range function.GetArgs() {
		rendered, err := renderNode(arg)
		if err != nil {
			return "", err
		}

		args = append(args, rendered)
	}

	for _, str := range function.GetStringArgs() {
		args = append(args, strconv.Quote(str))
	}

	return function.GetName() + "(" + strings.Join(args, ", ") + ")", nil
}

func renderBinary(bin *qdata.BinaryOp) (string, error) {
	symbol, ok := binOpSymbol(bin.GetOp())
	if !ok {
		return "", qerror.New(qerror.CodeInvalidArgument, "promdispatcher: unsupported binary operator")
	}

	lhs, err := renderNode(bin.GetLhs())
	if err != nil {
		return "", err
	}

	rhs, err := renderNode(bin.GetRhs())
	if err != nil {
		return "", err
	}

	// Parenthesize operands so operator precedence in the source plan is
	// preserved regardless of PromQL's own precedence rules.
	return fmt.Sprintf("(%s) %s%s (%s)", lhs, symbol, matchingClause(bin.GetMatching()), rhs), nil
}

// matchingClause renders the optional vector-matching modifiers, e.g.
// " on(a) group_left(b)".
func matchingClause(match *qdata.VectorMatch) string {
	if match == nil {
		return ""
	}

	var builder strings.Builder

	if on := match.GetOn(); len(on) > 0 {
		builder.WriteString(" on(" + strings.Join(on, ",") + ")")
	} else if ignoring := match.GetIgnoring(); len(ignoring) > 0 {
		builder.WriteString(" ignoring(" + strings.Join(ignoring, ",") + ")")
	}

	switch match.GetCardinality() {
	case qdata.CardinalityManyToOne:
		builder.WriteString(" group_left(" + strings.Join(match.GetInclude(), ",") + ")")
	case qdata.CardinalityOneToMany:
		builder.WriteString(" group_right(" + strings.Join(match.GetInclude(), ",") + ")")
	case qdata.CardinalityOneToOne:
	}

	return builder.String()
}

func matchOpSymbol(op qdata.MatchOp) (string, error) {
	switch op {
	case qdata.MatchEqual:
		return "=", nil
	case qdata.MatchNotEqual:
		return "!=", nil
	case qdata.MatchRegexp:
		return "=~", nil
	case qdata.MatchNotRegexp:
		return "!~", nil
	default:
		return "", qerror.New(qerror.CodeInvalidArgument, "promdispatcher: unknown match operator")
	}
}

// promDuration renders a Go duration as a Prometheus range duration, preferring
// whole seconds and falling back to milliseconds for sub-second windows.
func promDuration(dur time.Duration) string {
	if dur%time.Second == 0 {
		return strconv.FormatInt(int64(dur/time.Second), 10) + "s"
	}

	return strconv.FormatInt(dur.Milliseconds(), 10) + "ms"
}

// timeAggFunc maps a range-vector op to its PromQL function name.
func timeAggFunc(op qdata.TimeAggOp) (string, bool) {
	switch op {
	case qdata.TimeAggRate:
		return "rate", true
	case qdata.TimeAggIncrease:
		return "increase", true
	case qdata.TimeAggCountOverTime:
		return "count_over_time", true
	case qdata.TimeAggSumOverTime:
		return "sum_over_time", true
	case qdata.TimeAggAvgOverTime:
		return "avg_over_time", true
	case qdata.TimeAggMinOverTime:
		return "min_over_time", true
	case qdata.TimeAggMaxOverTime:
		return "max_over_time", true
	default:
		return "", false
	}
}

// aggFunc maps an aggregation op to its PromQL function name.
func aggFunc(operator qdata.AggOp) (string, bool) {
	name, ok := map[qdata.AggOp]string{
		qdata.AggSum:      "sum",
		qdata.AggAvg:      "avg",
		qdata.AggMin:      "min",
		qdata.AggMax:      "max",
		qdata.AggCount:    "count",
		qdata.AggQuantile: "quantile",
		qdata.AggTopK:     "topk",
		qdata.AggBottomK:  "bottomk",
		qdata.AggGroup:    "group",
		qdata.AggStddev:   "stddev",
		qdata.AggStdvar:   "stdvar",
	}[operator]

	return name, ok
}

// aggTakesParam reports whether an aggregation op takes a leading scalar param.
func aggTakesParam(op qdata.AggOp) bool {
	switch op {
	case qdata.AggQuantile, qdata.AggTopK, qdata.AggBottomK:
		return true
	default:
		return false
	}
}

// binOpSymbol maps a binary op to its PromQL operator symbol.
func binOpSymbol(operator qdata.BinOp) (string, bool) {
	symbol, ok := map[qdata.BinOp]string{
		qdata.BinAdd:    "+",
		qdata.BinSub:    "-",
		qdata.BinMul:    "*",
		qdata.BinDiv:    "/",
		qdata.BinMod:    "%",
		qdata.BinPow:    "^",
		qdata.BinEq:     "==",
		qdata.BinNe:     "!=",
		qdata.BinGt:     ">",
		qdata.BinLt:     "<",
		qdata.BinGe:     ">=",
		qdata.BinLe:     "<=",
		qdata.BinAnd:    "and",
		qdata.BinOr:     "or",
		qdata.BinUnless: "unless",
	}[operator]

	return symbol, ok
}
