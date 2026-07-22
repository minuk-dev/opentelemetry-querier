package lokidispatcher

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

// planToLogQL renders a structured qdata QueryPlan to a LogQL string. It fails
// closed (CodeInvalidArgument) on any node LogQL cannot express — a non-logs
// Select, a stream selector needing OR/NOT, or an unknown operator — so an
// unrenderable plan is rejected rather than mis-shipped.
func planToLogQL(plan *qdata.QueryPlan) (string, error) {
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
		return "", qerror.New(qerror.CodeInvalidArgument, "lokidispatcher: empty plan node")
	}
}

// renderSelect renders a LogQL stream selector plus any line filters. The stream
// selector is a flat conjunction of label matchers, so a filter needing OR/NOT
// cannot render.
func renderSelect(sel *qdata.Select) (string, error) {
	if sel.GetSignal() != qdata.SignalLogs {
		return "", qerror.New(qerror.CodeInvalidArgument,
			"lokidispatcher: Loki serves logs, not %s", sel.GetSignal())
	}

	matchers, ok := qdata.FlattenConjunction([]*qdata.Predicate{sel.GetFilter()})
	if !ok {
		return "", qerror.New(qerror.CodeInvalidArgument,
			"lokidispatcher: a LogQL stream selector cannot express OR/NOT composition")
	}

	if len(matchers) == 0 {
		return "", qerror.New(qerror.CodeInvalidArgument,
			"lokidispatcher: a LogQL stream selector needs at least one matcher")
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

	var builder strings.Builder

	builder.WriteString("{" + strings.Join(parts, ",") + "}")

	for _, line := range sel.GetLine() {
		symbol, lineErr := lineFilterSymbol(line.GetOp())
		if lineErr != nil {
			return "", lineErr
		}

		builder.WriteString(" " + symbol + " " + strconv.Quote(line.GetValue()))
	}

	return builder.String(), nil
}

func renderTimeAgg(agg *qdata.TimeAgg) (string, error) {
	funcName, ok := timeAggFunc(agg.GetOp())
	if !ok {
		return "", qerror.New(qerror.CodeInvalidArgument, "lokidispatcher: unsupported log-range function")
	}

	input, err := renderNode(agg.GetInput())
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s(%s[%s])", funcName, input, logqlDuration(agg.GetWindow().AsDuration())), nil
}

func renderAggregate(agg *qdata.Aggregate) (string, error) {
	funcName, ok := aggFunc(agg.GetOp())
	if !ok {
		return "", qerror.New(qerror.CodeInvalidArgument, "lokidispatcher: unsupported aggregation")
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
		return "", qerror.New(qerror.CodeInvalidArgument, "lokidispatcher: unsupported binary operator")
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
	// preserved regardless of LogQL's own precedence rules.
	return fmt.Sprintf("(%s) %s (%s)", lhs, symbol, rhs), nil
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
		return "", qerror.New(qerror.CodeInvalidArgument, "lokidispatcher: unknown match operator")
	}
}

// lineFilterSymbol maps a LineMatch op to a LogQL line-filter operator.
func lineFilterSymbol(op qdata.MatchOp) (string, error) {
	switch op {
	case qdata.MatchEqual:
		return "|=", nil
	case qdata.MatchNotEqual:
		return "!=", nil
	case qdata.MatchRegexp:
		return "|~", nil
	case qdata.MatchNotRegexp:
		return "!~", nil
	default:
		return "", qerror.New(qerror.CodeInvalidArgument, "lokidispatcher: unknown line-filter operator")
	}
}

// logqlDuration renders a Go duration as a LogQL range duration, preferring
// whole seconds and falling back to milliseconds for sub-second windows.
func logqlDuration(dur time.Duration) string {
	if dur%time.Second == 0 {
		return strconv.FormatInt(int64(dur/time.Second), 10) + "s"
	}

	return strconv.FormatInt(dur.Milliseconds(), 10) + "ms"
}

// timeAggFunc maps a range op to its LogQL log-range aggregation name. LogQL has
// no `increase`, so that op is unsupported here.
func timeAggFunc(operator qdata.TimeAggOp) (string, bool) {
	name, ok := map[qdata.TimeAggOp]string{
		qdata.TimeAggRate:          "rate",
		qdata.TimeAggCountOverTime: "count_over_time",
		qdata.TimeAggSumOverTime:   "sum_over_time",
		qdata.TimeAggAvgOverTime:   "avg_over_time",
		qdata.TimeAggMinOverTime:   "min_over_time",
		qdata.TimeAggMaxOverTime:   "max_over_time",
	}[operator]

	return name, ok
}

// aggFunc maps an aggregation op to its LogQL vector-aggregation name.
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

// binOpSymbol maps a binary op to its LogQL operator symbol.
func binOpSymbol(operator qdata.BinOp) (string, bool) {
	symbol, ok := map[qdata.BinOp]string{
		qdata.BinAdd: "+",
		qdata.BinSub: "-",
		qdata.BinMul: "*",
		qdata.BinDiv: "/",
		qdata.BinMod: "%",
		qdata.BinPow: "^",
		qdata.BinEq:  "==",
		qdata.BinNe:  "!=",
		qdata.BinGt:  ">",
		qdata.BinLt:  "<",
		qdata.BinGe:  ">=",
		qdata.BinLe:  "<=",
		qdata.BinAnd: "and",
		qdata.BinOr:  "or",
	}[operator]

	return symbol, ok
}
