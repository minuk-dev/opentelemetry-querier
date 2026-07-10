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

// stubRewriter is a DialectRewriter that records the predicates it received and
// returns a fixed expression, letting the test assert the processor dispatches by
// dialect and hands over the collected predicates.
type stubRewriter struct {
	dialect  string
	gotPreds []*qdata.LabelMatcher
}

func (s *stubRewriter) Dialect() string { return s.dialect }

func (s *stubRewriter) Enforce(_ string, preds []*qdata.LabelMatcher) (string, error) {
	s.gotPreds = preds

	return "rewritten-by-stub", nil
}

func TestProcessQueryDispatchesByDialect(t *testing.T) {
	t.Parallel()

	stub := &stubRewriter{dialect: "logql", gotPreds: nil}
	proc := queryrewrite.New(queryrewrite.Config{EnforceLabels: nil}, queryrewrite.WithRewriter(stub))

	query := &qdata.Query{
		Expr:             `{job="x"}`,
		Dialect:          "logql",
		EnforcedMatchers: []*qdata.LabelMatcher{{Name: "tenant", Op: qdata.MatchEqual, Value: "acme"}},
	}

	err := proc.ProcessQuery(context.Background(), query)
	if err != nil {
		t.Fatalf("ProcessQuery: %v", err)
	}

	if got := query.GetExpr(); got != "rewritten-by-stub" {
		t.Fatalf("expr = %q, want registered rewriter output", got)
	}

	if len(stub.gotPreds) != 1 || stub.gotPreds[0].GetValue() != "acme" {
		t.Fatalf("stub received preds = %+v, want the enforced tenant matcher", stub.gotPreds)
	}
}

func TestProcessQueryUnknownDialectWithEnforcementFailsClosed(t *testing.T) {
	t.Parallel()

	proc := queryrewrite.New(queryrewrite.Config{
		EnforceLabels: []queryrewrite.EnforceLabel{{Name: "tenant", Value: "acme", FromTenant: false}},
	})

	query := &qdata.Query{Expr: `SELECT * FROM t`, Dialect: "sql"}

	err := proc.ProcessQuery(context.Background(), query)
	if err == nil {
		t.Fatal("expected fail-closed error enforcing matchers on an unsupported dialect")
	}

	if got := query.GetExpr(); got != `SELECT * FROM t` {
		t.Fatalf("expr mutated on failure: %q", got)
	}
}

func TestProcessQueryUnknownDialectNoEnforcementPassesThrough(t *testing.T) {
	t.Parallel()

	proc := queryrewrite.New(queryrewrite.Config{EnforceLabels: nil})

	query := &qdata.Query{Expr: `SELECT * FROM t`, Dialect: "sql"}

	err := proc.ProcessQuery(context.Background(), query)
	if err != nil {
		t.Fatalf("ProcessQuery: %v", err)
	}

	if got := query.GetExpr(); got != `SELECT * FROM t` {
		t.Fatalf("unknown dialect was rewritten: %q", got)
	}
}
