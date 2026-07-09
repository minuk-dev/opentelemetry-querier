package queryrewrite_test

import (
	"context"
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/processor/queryrewrite"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

type rewriteCase struct {
	name  string
	cfg   queryrewrite.Config
	query *qdata.Query
	want  string
}

// testTenant is the resolved tenant id used across the rewrite cases.
const testTenant = "acme"

// withTenant records the resolved tenant id in the query's metadata, mirroring
// what the tenant processor does upstream.
func withTenant(q *qdata.Query) *qdata.Query {
	qdata.SetTenantID(q, testTenant)

	return q
}

func rewriteCases() []rewriteCase {
	tenantEnforce := queryrewrite.Config{
		EnforceLabels: []queryrewrite.EnforceLabel{{Name: "tenant_id", Value: "", FromTenant: true}},
	}

	return []rewriteCase{
		{
			name:  "injects tenant matcher",
			cfg:   tenantEnforce,
			query: withTenant(&qdata.Query{Expr: "up", Dialect: "promql"}),
			want:  `up{tenant_id="acme"}`,
		},
		{
			name: "injects into every selector",
			cfg:  tenantEnforce,
			query: withTenant(&qdata.Query{
				Expr:    `sum(rate(http_requests_total[5m])) / sum(rate(http_errors_total[5m]))`,
				Dialect: "promql",
			}),
			want: `sum(rate(http_requests_total{tenant_id="acme"}[5m])) / sum(rate(http_errors_total{tenant_id="acme"}[5m]))`,
		},
		{
			name:  "enforcement overrides user matcher",
			cfg:   tenantEnforce,
			query: withTenant(&qdata.Query{Expr: `up{tenant_id="evil"}`, Dialect: "promql"}),
			want:  `up{tenant_id="acme"}`,
		},
		{
			name: "enforced matchers from query",
			cfg:  queryrewrite.Config{EnforceLabels: nil},
			query: &qdata.Query{
				Expr:             "up",
				Dialect:          "promql",
				EnforcedMatchers: []*qdata.LabelMatcher{{Name: "namespace", Op: qdata.MatchEqual, Value: "prod"}},
			},
			want: `up{namespace="prod"}`,
		},
		{
			name:  "non-promql passes through",
			cfg:   tenantEnforce,
			query: withTenant(&qdata.Query{Expr: `{job="x"}`, Dialect: "logql"}),
			want:  `{job="x"}`,
		},
	}
}

func TestProcessQuery(t *testing.T) {
	t.Parallel()

	for _, testCase := range rewriteCases() {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			proc := queryrewrite.New(testCase.cfg)

			err := proc.ProcessQuery(context.Background(), testCase.query)
			if err != nil {
				t.Fatalf("ProcessQuery: %v", err)
			}

			if got := testCase.query.GetExpr(); got != testCase.want {
				t.Fatalf("expr = %q, want %q", got, testCase.want)
			}
		})
	}
}
