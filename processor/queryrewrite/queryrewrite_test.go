package queryrewrite

import (
	"context"
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

func TestProcessQuery_InjectsTenantMatcher(t *testing.T) {
	p := New(Config{EnforceLabels: []EnforceLabel{{Name: "tenant_id", FromTenant: true}}})
	q := &qdata.Query{Expr: "up", Dialect: "promql", TenantId: "acme"}

	if err := p.ProcessQuery(context.Background(), q); err != nil {
		t.Fatalf("ProcessQuery: %v", err)
	}
	if got, want := q.Expr, `up{tenant_id="acme"}`; got != want {
		t.Fatalf("expr = %q, want %q", got, want)
	}
}

func TestProcessQuery_InjectsIntoEverySelector(t *testing.T) {
	p := New(Config{EnforceLabels: []EnforceLabel{{Name: "tenant_id", FromTenant: true}}})
	q := &qdata.Query{
		Expr:     `sum(rate(http_requests_total[5m])) / sum(rate(http_errors_total[5m]))`,
		Dialect:  "promql",
		TenantId: "acme",
	}

	if err := p.ProcessQuery(context.Background(), q); err != nil {
		t.Fatalf("ProcessQuery: %v", err)
	}
	want := `sum(rate(http_requests_total{tenant_id="acme"}[5m])) / sum(rate(http_errors_total{tenant_id="acme"}[5m]))`
	if q.Expr != want {
		t.Fatalf("expr = %q, want %q", q.Expr, want)
	}
}

func TestProcessQuery_EnforcementOverridesUserMatcher(t *testing.T) {
	p := New(Config{EnforceLabels: []EnforceLabel{{Name: "tenant_id", FromTenant: true}}})
	q := &qdata.Query{Expr: `up{tenant_id="evil"}`, Dialect: "promql", TenantId: "acme"}

	if err := p.ProcessQuery(context.Background(), q); err != nil {
		t.Fatalf("ProcessQuery: %v", err)
	}
	if got, want := q.Expr, `up{tenant_id="acme"}`; got != want {
		t.Fatalf("expr = %q, want %q (enforcement must override user matcher)", got, want)
	}
}

func TestProcessQuery_EnforcedMatchersFromQuery(t *testing.T) {
	p := New(Config{})
	q := &qdata.Query{
		Expr:    "up",
		Dialect: "promql",
		EnforcedMatchers: []*qdata.LabelMatcher{
			{Name: "namespace", Op: qdata.MatchEqual, Value: "prod"},
		},
	}

	if err := p.ProcessQuery(context.Background(), q); err != nil {
		t.Fatalf("ProcessQuery: %v", err)
	}
	if got, want := q.Expr, `up{namespace="prod"}`; got != want {
		t.Fatalf("expr = %q, want %q", got, want)
	}
}

func TestProcessQuery_NonPromQLPassThrough(t *testing.T) {
	p := New(Config{EnforceLabels: []EnforceLabel{{Name: "tenant_id", FromTenant: true}}})
	q := &qdata.Query{Expr: `{job="x"}`, Dialect: "logql", TenantId: "acme"}

	if err := p.ProcessQuery(context.Background(), q); err != nil {
		t.Fatalf("ProcessQuery: %v", err)
	}
	if got, want := q.Expr, `{job="x"}`; got != want {
		t.Fatalf("expr = %q, want unchanged %q", got, want)
	}
}
