package pipeline_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/minuk-dev/opentelemetry-querier/processor/simpleauthzprocessor"
	"github.com/minuk-dev/opentelemetry-querier/processor/tenantprocessor"
)

// TestSimpleAuthz adds the per-subject authorization processor after the tenant
// processor and asserts the policy decisions end to end: an allowed subject
// passes through, a denied subject is rejected with 403, and an unmatched
// subject hits default_effect (deny) and fails closed. The scope-down effect
// (an allow rule that registers enforced matchers) is observed once queryrewrite
// is in the chain — see TestQueryRewrite and TestFullChain.
func TestSimpleAuthz(t *testing.T) {
	t.Parallel()

	tenant := tenantprocessor.New(tenantprocessor.Config{
		Header: tenantHeader, Default: "", Required: false, EnforceLabel: "",
	})

	// Policy: admin is allowed unscoped, blocked is explicitly denied, and any
	// other subject falls through to default_effect (deny).
	authz := simpleauthzprocessor.New(simpleauthzprocessor.Config{
		SubjectHeader:  "X-Scope-User",
		FromTenant:     false,
		DefaultSubject: "",
		Required:       false,
		DefaultEffect:  simpleauthzprocessor.EffectDeny,
		Rules: []simpleauthzprocessor.Rule{
			{Subjects: []string{"admin"}, Effect: simpleauthzprocessor.EffectAllow, EnforceLabels: nil},
			{Subjects: []string{"blocked"}, Effect: simpleauthzprocessor.EffectDeny, EnforceLabels: nil},
		},
	})

	t.Run("allowed subject passes through", func(t *testing.T) {
		t.Parallel()

		upstream := newUpstream(t)
		base := front(t, upstream, tenant, authz)

		code, body := getWith(t, base, map[string]string{tenantHeader: "acme", "X-Scope-User": "admin"})

		assert.Equal(t, http.StatusOK, code, "body: %s", body)
		assert.Equal(t, `{__name__="up"}`, upstream.gotQuery(), "the allowed query reaches the upstream")
	})

	t.Run("denied subject -> 403", func(t *testing.T) {
		t.Parallel()

		upstream := newUpstream(t)
		base := front(t, upstream, tenant, authz)

		code, body := getWith(t, base, map[string]string{tenantHeader: "acme", "X-Scope-User": "blocked"})

		assert.Equal(t, http.StatusForbidden, code, "body: %s", body)
		assert.Empty(t, upstream.gotQuery(), "a denied query never reaches the upstream")
	})

	t.Run("unmatched subject fails closed -> 403", func(t *testing.T) {
		t.Parallel()

		upstream := newUpstream(t)
		base := front(t, upstream, tenant, authz)

		code, body := getWith(t, base, map[string]string{tenantHeader: "acme", "X-Scope-User": "stranger"})

		assert.Equal(t, http.StatusForbidden, code,
			"no rule matches, so default_effect (deny) applies; body: %s", body)
		assert.Empty(t, upstream.gotQuery())
	})
}
