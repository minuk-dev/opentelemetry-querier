package pipeline_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/minuk-dev/opentelemetry-querier/processor/queryrewriteprocessor"
	"github.com/minuk-dev/opentelemetry-querier/processor/simpleauthzprocessor"
	"github.com/minuk-dev/opentelemetry-querier/processor/tenantprocessor"
)

// plainTenant is a tenant processor that resolves the id but registers no
// isolation matcher of its own, so a test can attribute the rendered matchers to
// queryrewrite alone.
func plainTenant() *tenantprocessor.Processor {
	return tenantprocessor.New(tenantprocessor.Config{
		Header: tenantHeader, Default: "", Required: false, EnforceLabel: "",
	})
}

// TestQueryRewriteStaticAndFromTenant adds the query-rewrite processor after the
// tenant processor and asserts its statically-configured matchers — a fixed one
// and a from_tenant one — are AND-ed into the plan and appear in the rendered
// upstream query.
func TestQueryRewriteStaticAndFromTenant(t *testing.T) {
	t.Parallel()

	rewrite := queryrewriteprocessor.New(queryrewriteprocessor.Config{
		EnforceLabels: []queryrewriteprocessor.EnforceLabel{
			{Name: "tenant_id", Value: "", FromTenant: true},
			{Name: "env", Value: "prod", FromTenant: false},
		},
	})

	upstream := newUpstream(t)
	base := front(t, upstream, plainTenant(), rewrite)

	code, body := getWith(t, base, map[string]string{tenantHeader: "acme"})

	assert.Equal(t, http.StatusOK, code, "body: %s", body)
	// The selector renders as a sorted flat conjunction.
	assert.Equal(t, `{__name__="up",env="prod",tenant_id="acme"}`, upstream.gotQuery(),
		"the enforced matchers are AND-ed into the rendered upstream query")
}

// TestQueryRewriteEnforcesTenantMatcher asserts that the isolation matcher the
// tenant processor registers is woven into the rendered query once queryrewrite
// runs — proving processor order (tenant before queryrewrite) matters.
func TestQueryRewriteEnforcesTenantMatcher(t *testing.T) {
	t.Parallel()

	// tenant registers tenant_id=<id>; queryrewrite has no config of its own but
	// still weaves the already-registered matcher into the plan.
	tenant := tenantprocessor.New(tenantprocessor.Config{
		Header: tenantHeader, Default: "", Required: false, EnforceLabel: "tenant_id",
	})
	rewrite := queryrewriteprocessor.New(queryrewriteprocessor.Config{EnforceLabels: nil})

	upstream := newUpstream(t)
	base := front(t, upstream, tenant, rewrite)

	code, body := getWith(t, base, map[string]string{tenantHeader: "acme"})

	assert.Equal(t, http.StatusOK, code, "body: %s", body)
	assert.Equal(t, `{__name__="up",tenant_id="acme"}`, upstream.gotQuery(),
		"the tenant isolation matcher is now enforced into the query")
}

// TestQueryRewriteEnforcesScopeDown asserts that a scope-down matcher registered
// by simpleauthz (alice is allowed only within namespace=team-a) is enforced
// into the rendered query through queryrewrite.
func TestQueryRewriteEnforcesScopeDown(t *testing.T) {
	t.Parallel()

	authz := simpleauthzprocessor.New(simpleauthzprocessor.Config{
		SubjectHeader:  "X-Scope-User",
		FromTenant:     false,
		DefaultSubject: "",
		Required:       false,
		DefaultEffect:  simpleauthzprocessor.EffectDeny,
		Rules: []simpleauthzprocessor.Rule{{
			Subjects: []string{"alice"},
			Effect:   simpleauthzprocessor.EffectAllow,
			EnforceLabels: []simpleauthzprocessor.EnforceLabel{
				{Name: "namespace", Value: "team-a", FromTenant: false},
			},
		}},
	})
	rewrite := queryrewriteprocessor.New(queryrewriteprocessor.Config{EnforceLabels: nil})

	upstream := newUpstream(t)
	base := front(t, upstream, plainTenant(), authz, rewrite)

	code, body := getWith(t, base, map[string]string{tenantHeader: "acme", "X-Scope-User": "alice"})

	assert.Equal(t, http.StatusOK, code, "body: %s", body)
	assert.Equal(t, `{__name__="up",namespace="team-a"}`, upstream.gotQuery(),
		"the subject scope-down matcher is enforced into the query")
}
