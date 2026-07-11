package qdata_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

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
