package qdata_test

import (
	"testing"

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
