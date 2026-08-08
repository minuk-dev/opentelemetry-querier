package pipeline_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/minuk-dev/opentelemetry-querier/processor/tenantprocessor"
)

// TestTenant adds the tenant processor to the chain and asserts that the
// X-Scope-OrgID header resolves the tenant end to end: the resolved id is
// forwarded to the upstream by the dispatcher, and the isolation matcher is
// registered but — with no queryrewrite yet — not yet woven into the rendered
// query (that is the query-rewrite step's job).
func TestTenant(t *testing.T) {
	t.Parallel()

	tenant := tenantprocessor.New(tenantprocessor.Config{
		Header:       tenantHeader,
		Default:      "",
		Required:     false,
		EnforceLabel: "tenant_id",
	})

	t.Run("X-Scope-OrgID resolves the tenant and is forwarded upstream", func(t *testing.T) {
		t.Parallel()

		upstream := newUpstream(t)
		base := front(t, upstream, tenant)

		code, body := getWith(t, base, map[string]string{tenantHeader: "acme"})

		assert.Equal(t, http.StatusOK, code, "body: %s", body)
		assert.Equal(t, "acme", upstream.gotTenant(), "the dispatcher forwards the resolved tenant id")
		assert.Equal(t, `{__name__="up"}`, upstream.gotQuery(),
			"the isolation matcher is registered but not enforced into the query without queryrewrite")
	})

	t.Run("no header leaves the tenant unset", func(t *testing.T) {
		t.Parallel()

		upstream := newUpstream(t)
		base := front(t, upstream, tenant)

		code, body := getWith(t, base, nil)

		assert.Equal(t, http.StatusOK, code, "body: %s", body)
		assert.Empty(t, upstream.gotTenant(), "no tenant resolves, so none is forwarded")
	})
}
