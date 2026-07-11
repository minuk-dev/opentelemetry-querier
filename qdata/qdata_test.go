package qdata_test

import (
	"testing"

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

			if got := qdata.QueryDialect(testCase.query); got != testCase.want {
				t.Fatalf("QueryDialect = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestKnownDialect(t *testing.T) {
	t.Parallel()

	known := []string{qdata.DialectPromQL, qdata.DialectLogQL, qdata.DialectLucene, qdata.DialectSQL}
	for _, dialect := range known {
		if !qdata.KnownDialect(dialect) {
			t.Errorf("KnownDialect(%q) = false, want true", dialect)
		}
	}

	for _, dialect := range []string{"", "cypher", "PromQL"} {
		if qdata.KnownDialect(dialect) {
			t.Errorf("KnownDialect(%q) = true, want false", dialect)
		}
	}
}
