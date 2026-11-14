package pipeline_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/minuk-dev/opentelemetry-querier/processor/tenantprocessor"
)

// TestTenant forwards a resolved tenant without configuring label isolation.
func TestTenant(t *testing.T) {
	t.Parallel()

	tenant := tenantprocessor.New(tenantprocessor.Config{
		Header:       tenantHeader,
		Default:      "",
		Required:     false,
		EnforceLabel: "",
	})

	t.Run("X-Scope-OrgID resolves the tenant and is forwarded upstream", func(t *testing.T) {
		t.Parallel()

		upstream := newUpstream(t)
		base := front(t, upstream, tenant)

		code, body := getWith(t, base, map[string]string{tenantHeader: "acme"})

		assert.Equal(t, http.StatusOK, code, "body: %s", body)
		assert.Equal(t, "acme", upstream.gotTenant(), "the dispatcher forwards the resolved tenant id")
		assert.Equal(t, `{__name__="up"}`, upstream.gotQuery(),
			"tenant forwarding without label isolation does not require queryrewrite")
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
