package qdata_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

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

func TestQuerySignals(t *testing.T) {
	t.Parallel()

	crossPlan := qdata.Plan(qdata.BinaryNode(qdata.BinDiv,
		qdata.SelectNode(qdata.SignalLogs, nil), selectMetrics("x"), nil))

	t.Run("plan is authoritative over signal fields", func(t *testing.T) {
		t.Parallel()

		query := &qdata.Query{Signal: qdata.SignalSpans, Plan: crossPlan}
		assert.Equal(t, []qdata.Signal{qdata.SignalMetrics, qdata.SignalLogs}, qdata.QuerySignals(query))
	})

	t.Run("falls back to explicit signals set, deduped and sorted", func(t *testing.T) {
		t.Parallel()

		query := &qdata.Query{Signals: []qdata.Signal{qdata.SignalLogs, qdata.SignalMetrics, qdata.SignalLogs}}
		assert.Equal(t, []qdata.Signal{qdata.SignalMetrics, qdata.SignalLogs}, qdata.QuerySignals(query))
	})

	t.Run("falls back to single signal field", func(t *testing.T) {
		t.Parallel()

		query := &qdata.Query{Signal: qdata.SignalMetrics}
		assert.Equal(t, []qdata.Signal{qdata.SignalMetrics}, qdata.QuerySignals(query))
	})
}

func TestSyncPlanSignals(t *testing.T) {
	t.Parallel()

	t.Run("mirrors plan signals onto the query", func(t *testing.T) {
		t.Parallel()

		query := &qdata.Query{Plan: qdata.Plan(qdata.BinaryNode(qdata.BinDiv,
			qdata.SelectNode(qdata.SignalLogs, nil), selectMetrics("x"), nil))}
		qdata.SyncPlanSignals(query)
		assert.Equal(t, []qdata.Signal{qdata.SignalMetrics, qdata.SignalLogs}, query.GetSignals())
	})

	t.Run("no plan leaves signals untouched", func(t *testing.T) {
		t.Parallel()

		query := &qdata.Query{Signal: qdata.SignalMetrics}
		qdata.SyncPlanSignals(query)
		assert.Empty(t, query.GetSignals())
	})
}

func TestValidateTable(t *testing.T) {
	t.Parallel()

	row := qdata.NewRow

	cases := []struct {
		name    string
		table   *qdata.Table
		wantErr bool
	}{
		{
			name:    "rows within schema",
			table:   qdata.NewTable([]string{"a", "b"}, row("a", qdata.Str("1"), "b", qdata.Str("2"))),
			wantErr: false,
		},
		{
			name:    "empty schema skips membership check",
			table:   qdata.NewTable(nil, row("anything", qdata.Str("x"))),
			wantErr: false,
		},
		{
			name:    "column absent from schema",
			table:   qdata.NewTable([]string{"a"}, row("a", qdata.Str("1"), "z", qdata.Str("9"))),
			wantErr: true,
		},
		{
			name:    "nil row with schema",
			table:   qdata.NewTable([]string{"a"}, nil),
			wantErr: true,
		},
		{
			name:    "nil row without schema",
			table:   qdata.NewTable(nil, nil),
			wantErr: true,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := qdata.ValidateTable(testCase.table)
			if testCase.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestTableResult(t *testing.T) {
	t.Parallel()

	table := qdata.NewTable([]string{"metric", "message"},
		qdata.NewRow("metric", qdata.Double(0.5), "message", qdata.Str("boom")))
	result := qdata.TableResult(table)

	require.NotNil(t, result.GetTable())
	assert.Equal(t, []string{"metric", "message"}, result.GetTable().GetColumns())
	assert.Len(t, result.GetTable().GetRows(), 1)
}

func TestValueJSON(t *testing.T) {
	t.Parallel()

	instant := time.Date(2023, 11, 14, 22, 13, 20, 0, time.UTC)

	cases := []struct {
		name  string
		value *qdata.Value
		want  any
	}{
		{"double", qdata.Double(0.5), 0.5},
		{"int", qdata.Int(-7), int64(-7)},
		{"uint", qdata.Uint(9), uint64(9)},
		{"string", qdata.Str("hi"), "hi"},
		{"bool", qdata.Bool(true), true},
		{"timestamp", qdata.Timestamp(instant), "2023-11-14T22:13:20Z"},
		{"json passthrough", qdata.JSON(`{"k":1}`), json.RawMessage(`{"k":1}`)},
		{"invalid json falls back to string", qdata.JSON("{oops"), "{oops"},
		{"empty json falls back to string", qdata.JSON(""), ""},
		{"array recurses", qdata.Array(qdata.Int(1), qdata.Str("x")), []any{int64(1), "x"}},
		{"nil value", nil, nil},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testCase.want, qdata.ValueJSON(testCase.value))
		})
	}
}

func TestRenderTable(t *testing.T) {
	t.Parallel()

	t.Run("schema drives column order and absent cells are null", func(t *testing.T) {
		t.Parallel()

		table := qdata.NewTable([]string{"metric", "message"},
			qdata.NewRow("metric", qdata.Double(0.5), "message", qdata.Str("boom")),
			qdata.NewRow("metric", qdata.Double(1)),
		)

		wire := qdata.RenderTable(table)

		assert.Equal(t, []string{"metric", "message"}, wire.Columns)
		assert.Equal(t, [][]any{{0.5, "boom"}, {float64(1), nil}}, wire.Rows)
	})

	t.Run("row keys outside the declared schema are appended, not dropped", func(t *testing.T) {
		t.Parallel()

		// "extra" is absent from the schema; it must still be rendered as a
		// trailing column rather than silently lost.
		table := qdata.NewTable([]string{"metric"},
			qdata.NewRow("metric", qdata.Double(0.5), "extra", qdata.Str("kept")),
		)

		wire := qdata.RenderTable(table)

		assert.Equal(t, []string{"metric", "extra"}, wire.Columns)
		assert.Equal(t, [][]any{{0.5, "kept"}}, wire.Rows)
	})

	t.Run("schema-less table derives columns from row keys in first-seen order", func(t *testing.T) {
		t.Parallel()

		table := qdata.NewTable(nil,
			qdata.NewRow("b", qdata.Str("1"), "a", qdata.Str("2")),
			qdata.NewRow("c", qdata.Str("3")),
		)

		wire := qdata.RenderTable(table)

		assert.Equal(t, []string{"b", "a", "c"}, wire.Columns)
		assert.Equal(t, [][]any{{"1", "2", nil}, {nil, nil, "3"}}, wire.Rows)
	})

	t.Run("empty table renders non-nil empty arrays", func(t *testing.T) {
		t.Parallel()

		wire := qdata.RenderTable(qdata.NewTable(nil))

		assert.Equal(t, []string{}, wire.Columns)
		assert.Equal(t, [][]any{}, wire.Rows)

		encoded, err := json.Marshal(wire)
		require.NoError(t, err)
		assert.JSONEq(t, `{"columns":[],"rows":[]}`, string(encoded))
	})
}
