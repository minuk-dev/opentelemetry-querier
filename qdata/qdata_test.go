package qdata_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

func TestQueryDialect(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		query *qdata.Query
		want  string
	}{
		{
			name:  "empty defaults to promql",
			query: &qdata.Query{Expr: "up"},
			want:  qdata.DialectPromQL,
		},
		{
			name:  "explicit dialect kept",
			query: &qdata.Query{Expr: `{job="x"}`, Dialect: qdata.DialectLogQL},
			want:  qdata.DialectLogQL,
		},
		{
			name:  "unknown tag kept verbatim",
			query: &qdata.Query{Dialect: "cypher"},
			want:  "cypher",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testCase.want, qdata.QueryDialect(testCase.query))
		})
	}
}

func TestKnownDialect(t *testing.T) {
	t.Parallel()

	for _, dialect := range []string{qdata.DialectPromQL, qdata.DialectLogQL, qdata.DialectLucene, qdata.DialectSQL} {
		assert.Truef(t, qdata.KnownDialect(dialect), "KnownDialect(%q)", dialect)
	}

	for _, dialect := range []string{"", "cypher", "PromQL"} {
		assert.Falsef(t, qdata.KnownDialect(dialect), "KnownDialect(%q)", dialect)
	}
}

func leaf(name, value string) *qdata.Predicate {
	return qdata.LeafPredicate(&qdata.LabelMatcher{Name: name, Op: qdata.MatchEqual, Value: value})
}

func TestValidatePredicate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		pred    *qdata.Predicate
		wantErr bool
	}{
		{name: "leaf", pred: leaf("tenant", "acme"), wantErr: false},
		{name: "and of leaves", pred: qdata.BoolPredicate(qdata.BoolAnd, leaf("a", "1"), leaf("b", "2")), wantErr: false},
		{name: "or of leaves", pred: qdata.BoolPredicate(qdata.BoolOr, leaf("a", "1"), leaf("b", "2")), wantErr: false},
		{name: "not one operand", pred: qdata.BoolPredicate(qdata.BoolNot, leaf("a", "1")), wantErr: false},
		{name: "nested", wantErr: false, pred: qdata.BoolPredicate(qdata.BoolAnd, leaf("a", "1"),
			qdata.BoolPredicate(qdata.BoolOr, leaf("b", "2"), leaf("c", "3")))},
		{name: "nil", pred: nil, wantErr: true},
		{name: "empty node", pred: &qdata.Predicate{}, wantErr: true},
		{name: "nil leaf", pred: qdata.LeafPredicate(nil), wantErr: true},
		{name: "not zero operands", pred: qdata.BoolPredicate(qdata.BoolNot), wantErr: true},
		{name: "not two operands", pred: qdata.BoolPredicate(qdata.BoolNot, leaf("a", "1"), leaf("b", "2")), wantErr: true},
		{name: "and zero operands", pred: qdata.BoolPredicate(qdata.BoolAnd), wantErr: true},
		{name: "invalid descendant", pred: qdata.BoolPredicate(qdata.BoolAnd, qdata.LeafPredicate(nil)), wantErr: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := qdata.ValidatePredicate(testCase.pred)
			if testCase.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestFlattenConjunction(t *testing.T) {
	t.Parallel()

	t.Run("pure conjunction flattens", func(t *testing.T) {
		t.Parallel()

		preds := []*qdata.Predicate{
			leaf("tenant", "acme"),
			qdata.BoolPredicate(qdata.BoolAnd, leaf("env", "prod"), leaf("region", "eu")),
		}

		matchers, ok := qdata.FlattenConjunction(preds)
		require.True(t, ok, "a pure AND-of-leaves forest should flatten")
		assert.Len(t, matchers, 3)
	})

	t.Run("empty forest ok with no matchers", func(t *testing.T) {
		t.Parallel()

		matchers, ok := qdata.FlattenConjunction(nil)
		require.True(t, ok)
		assert.Empty(t, matchers)
	})

	t.Run("or fails to flatten", func(t *testing.T) {
		t.Parallel()

		preds := []*qdata.Predicate{qdata.BoolPredicate(qdata.BoolOr, leaf("a", "1"), leaf("b", "2"))}
		_, ok := qdata.FlattenConjunction(preds)
		assert.False(t, ok, "OR must not flatten to a conjunction")
	})

	t.Run("not fails to flatten", func(t *testing.T) {
		t.Parallel()

		preds := []*qdata.Predicate{qdata.BoolPredicate(qdata.BoolNot, leaf("a", "1"))}
		_, ok := qdata.FlattenConjunction(preds)
		assert.False(t, ok, "NOT must not flatten to a conjunction")
	})
}

// selectMetrics builds a Select node over metrics filtered by __name__=metric.
func selectMetrics(metric string) *qdata.Node {
	return qdata.SelectNode(qdata.SignalMetrics, leaf("__name__", metric))
}

func TestValidatePlan(t *testing.T) {
	t.Parallel()

	metricX := selectMetrics("x")
	rate := qdata.TimeAggNode(qdata.TimeAggRate, time.Minute, selectMetrics("http_requests_total"))
	sumByRate := qdata.AggregateNode(qdata.AggSum, []string{"job"}, nil, 0, rate)
	binaryDiv := qdata.BinaryNode(qdata.BinDiv, selectMetrics("a"), selectMetrics("b"), nil)
	function := qdata.FunctionNode("abs", []*qdata.Node{metricX})

	badTimeAggOp := qdata.TimeAggNode(0, time.Minute, metricX)
	badTimeAggWindow := qdata.TimeAggNode(qdata.TimeAggRate, 0, metricX)
	badTimeAggInput := qdata.TimeAggNode(qdata.TimeAggRate, time.Minute, nil)
	badAggOp := qdata.AggregateNode(0, nil, nil, 0, metricX)
	badAggGrouping := qdata.AggregateNode(qdata.AggSum, []string{"a"}, []string{"b"}, 0, metricX)
	badBinary := qdata.BinaryNode(qdata.BinDiv, selectMetrics("a"), nil, nil)
	badFilter := qdata.SelectNode(qdata.SignalMetrics, qdata.LeafPredicate(nil))

	cases := []struct {
		name    string
		plan    *qdata.QueryPlan
		wantErr bool
	}{
		{name: "select leaf", plan: qdata.Plan(selectMetrics("up")), wantErr: false},
		{name: "select nil filter", plan: qdata.Plan(qdata.SelectNode(qdata.SignalLogs, nil)), wantErr: false},
		{name: "rate over select", plan: qdata.Plan(rate), wantErr: false},
		{name: "sum by rate", plan: qdata.Plan(sumByRate), wantErr: false},
		{name: "binary div", plan: qdata.Plan(binaryDiv), wantErr: false},
		{name: "literal", plan: qdata.Plan(qdata.LiteralNode(1.5)), wantErr: false},
		{name: "function", plan: qdata.Plan(function), wantErr: false},

		{name: "nil plan", plan: nil, wantErr: true},
		{name: "nil root", plan: qdata.Plan(nil), wantErr: true},
		{name: "empty node", plan: qdata.Plan(&qdata.Node{}), wantErr: true},
		{name: "time_agg unspecified op", plan: qdata.Plan(badTimeAggOp), wantErr: true},
		{name: "time_agg zero window", plan: qdata.Plan(badTimeAggWindow), wantErr: true},
		{name: "time_agg nil input", plan: qdata.Plan(badTimeAggInput), wantErr: true},
		{name: "aggregate unspecified op", plan: qdata.Plan(badAggOp), wantErr: true},
		{name: "aggregate by and without", plan: qdata.Plan(badAggGrouping), wantErr: true},
		{name: "function empty name", plan: qdata.Plan(qdata.FunctionNode("", nil)), wantErr: true},
		{name: "binary missing operand", plan: qdata.Plan(badBinary), wantErr: true},
		{name: "invalid nested filter", plan: qdata.Plan(badFilter), wantErr: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := qdata.ValidatePlan(testCase.plan)
			if testCase.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestPlanSignals(t *testing.T) {
	t.Parallel()

	t.Run("single signal", func(t *testing.T) {
		t.Parallel()

		plan := qdata.Plan(qdata.TimeAggNode(qdata.TimeAggRate, time.Minute, selectMetrics("x")))
		assert.Equal(t, []qdata.Signal{qdata.SignalMetrics}, qdata.PlanSignals(plan))
	})

	t.Run("distinct signals across a binary op, sorted", func(t *testing.T) {
		t.Parallel()

		plan := qdata.Plan(qdata.BinaryNode(qdata.BinDiv,
			qdata.SelectNode(qdata.SignalLogs, nil),
			selectMetrics("x"), nil))
		// SignalMetrics(1) sorts before SignalLogs(2).
		assert.Equal(t, []qdata.Signal{qdata.SignalMetrics, qdata.SignalLogs}, qdata.PlanSignals(plan))
	})

	t.Run("empty plan yields no signals", func(t *testing.T) {
		t.Parallel()

		assert.Empty(t, qdata.PlanSignals(qdata.Plan(nil)))
	})
}
