package athenzauthextension //nolint:testpackage // Injects a role verifier.

import (
	"net/http"
	"testing"
	"time"

	"github.com/AthenZ/athenz-authorizer/v5/role"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/stretchr/testify/require"
)

func TestVerifyRoleToken(t *testing.T) {
	t.Parallel()

	for _, scenario := range []struct {
		name, domain string
		verifyErr    error
		stale        bool
		want         qerror.Code
	}{
		{"allowed", "team", nil, false, qerror.CodeInternal},
		{"wrong domain", "other", nil, false, qerror.CodePermissionDenied},
		{"invalid token", "team", role.ErrRoleTokenInvalid, false, qerror.CodeUnauthenticated},
		{"stale keys", "team", nil, true, qerror.CodeUnavailable},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			auth := new(Authenticator)
			auth.cfg.Domain, auth.cfg.Tenant, auth.cfg.MaxKeyAge = "team", "a", time.Minute

			auth.verify = func(string) (*role.Token, error) {
				token := new(role.Token)
				token.Domain, token.Principal, token.Roles = scenario.domain, "alice", []string{"reader"}

				return token, scenario.verifyErr
			}
			if !scenario.stale {
				auth.lastRefresh.Store(time.Now().UnixNano())
			}

			principal, err := auth.Authenticate(t.Context(), http.Header{"Athenz-Role-Auth": {"role-token"}})
			if scenario.want != qerror.CodeInternal {
				require.Equal(t, scenario.want, qerror.CodeOf(err))

				return
			}

			require.NoError(t, err)
			require.Equal(t, "alice", principal.Principal)
			require.Equal(t, []string{"reader"}, principal.Roles)
			require.Equal(t, []string{"a"}, principal.Tenants)
		})
	}
}
