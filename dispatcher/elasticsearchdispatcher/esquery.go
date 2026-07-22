package elasticsearchdispatcher

import (
	qdatav1 "github.com/minuk-dev/opentelemetry-querier/gen/qdata/v1"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// messageField is the document field a log line filter matches against.
const messageField = "message"

// planToESQuery renders a structured qdata QueryPlan to an Elasticsearch query
// DSL object. Elasticsearch serves log search, so the plan must be a single
// Select (metric aggregations over ES are not yet supported); the Select's
// predicate tree maps directly onto ES boolean queries — unlike PromQL/LogQL,
// ES expresses OR/NOT natively, so no flattening is needed. It fails closed
// (CodeInvalidArgument) on a non-logs Select or a non-Select root.
func planToESQuery(plan *qdata.QueryPlan) (map[string]any, error) {
	sel, ok := plan.GetRoot().GetOp().(*qdatav1.Node_Select)
	if !ok {
		return nil, qerror.New(qerror.CodeInvalidArgument,
			"elasticsearchdispatcher: only a Select plan is supported (no metric aggregation over Elasticsearch)")
	}

	return renderSelect(sel.Select)
}

func renderSelect(sel *qdata.Select) (map[string]any, error) {
	if sel.GetSignal() != qdata.SignalLogs {
		return nil, qerror.New(qerror.CodeInvalidArgument,
			"elasticsearchdispatcher: Elasticsearch serves logs, not %s", sel.GetSignal())
	}

	clauses := make([]map[string]any, 0, 1+len(sel.GetLine()))

	if filter := sel.GetFilter(); filter != nil {
		clause, err := predicateToES(filter)
		if err != nil {
			return nil, err
		}

		clauses = append(clauses, clause)
	}

	for _, line := range sel.GetLine() {
		clause, err := lineToES(line)
		if err != nil {
			return nil, err
		}

		clauses = append(clauses, clause)
	}

	switch len(clauses) {
	case 0:
		// An empty Select selects every document — Elasticsearch can express this.
		return map[string]any{"match_all": map[string]any{}}, nil
	case 1:
		return clauses[0], nil
	default:
		return boolClause("must", anySlice(clauses)), nil
	}
}

// anySlice widens a slice of clauses to []any for embedding in a bool query.
func anySlice(clauses []map[string]any) []any {
	out := make([]any, len(clauses))
	for i, clause := range clauses {
		out[i] = clause
	}

	return out
}

// predicateToES maps a predicate tree onto Elasticsearch's boolean query, which
// supports AND/OR/NOT natively (must / should+minimum_should_match / must_not).
func predicateToES(pred *qdata.Predicate) (map[string]any, error) {
	switch node := pred.GetNode().(type) {
	case *qdatav1.Predicate_Leaf:
		return matcherToES(node.Leaf)
	case *qdatav1.Predicate_BoolExpr:
		return boolExprToES(node.BoolExpr)
	default:
		return nil, qerror.New(qerror.CodeInvalidArgument, "elasticsearchdispatcher: empty predicate node")
	}
}

func boolExprToES(expr *qdata.BoolExpr) (map[string]any, error) {
	operands := make([]any, 0, len(expr.GetOperands()))

	for _, operand := range expr.GetOperands() {
		clause, err := predicateToES(operand)
		if err != nil {
			return nil, err
		}

		operands = append(operands, clause)
	}

	switch expr.GetOp() {
	case qdata.BoolAnd:
		return boolClause("must", operands), nil
	case qdata.BoolOr:
		return map[string]any{
			"bool": map[string]any{"should": operands, "minimum_should_match": 1},
		}, nil
	case qdata.BoolNot:
		return boolClause("must_not", operands), nil
	default:
		return nil, qerror.New(qerror.CodeInvalidArgument, "elasticsearchdispatcher: unknown bool op")
	}
}

// matcherToES maps one label matcher onto a term/regexp query, wrapping the
// negated operators in a must_not.
func matcherToES(matcher *qdata.LabelMatcher) (map[string]any, error) {
	switch matcher.GetOp() {
	case qdata.MatchEqual:
		return termClause(matcher.GetName(), matcher.GetValue()), nil
	case qdata.MatchNotEqual:
		return boolClause("must_not", []any{termClause(matcher.GetName(), matcher.GetValue())}), nil
	case qdata.MatchRegexp:
		return regexpClause(matcher.GetName(), matcher.GetValue()), nil
	case qdata.MatchNotRegexp:
		return boolClause("must_not", []any{regexpClause(matcher.GetName(), matcher.GetValue())}), nil
	default:
		return nil, qerror.New(qerror.CodeInvalidArgument, "elasticsearchdispatcher: unknown match operator")
	}
}

// lineToES maps a log line filter onto a match_phrase / regexp query over the
// message field.
func lineToES(line *qdata.LineMatch) (map[string]any, error) {
	switch line.GetOp() {
	case qdata.MatchEqual:
		return matchPhraseClause(line.GetValue()), nil
	case qdata.MatchNotEqual:
		return boolClause("must_not", []any{matchPhraseClause(line.GetValue())}), nil
	case qdata.MatchRegexp:
		return regexpClause(messageField, line.GetValue()), nil
	case qdata.MatchNotRegexp:
		return boolClause("must_not", []any{regexpClause(messageField, line.GetValue())}), nil
	default:
		return nil, qerror.New(qerror.CodeInvalidArgument, "elasticsearchdispatcher: unknown line-filter operator")
	}
}

func boolClause(occurrence string, clauses []any) map[string]any {
	return map[string]any{"bool": map[string]any{occurrence: clauses}}
}

func termClause(field, value string) map[string]any {
	return map[string]any{"term": map[string]any{field: value}}
}

func regexpClause(field, value string) map[string]any {
	return map[string]any{"regexp": map[string]any{field: value}}
}

func matchPhraseClause(value string) map[string]any {
	return map[string]any{"match_phrase": map[string]any{messageField: value}}
}
