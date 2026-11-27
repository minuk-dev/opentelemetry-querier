package athenzauthzprocessor //nolint:testpackage // Injects a signed policy fixture.

import (
	"context"
	"testing"
	"time"

	"github.com/AthenZ/athenz-authorizer/v5/policy"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/stretchr/testify/require"
)

func TestAuthorizeQuery(t *testing.T) {
	t.Parallel()

	for _, scenario := range []struct {
		name                   string
		checkErr               error
		allowedIdentity, fresh bool
		want                   qerror.Code
	}{
		{"allowed", nil, true, true, qerror.CodeInternal},
		{"explicit deny", policy.ErrDenyByPolicy, true, true, qerror.CodePermissionDenied},
		{"unknown resource", policy.ErrNoMatch, true, true, qerror.CodePermissionDenied},
		{"wrong identity", nil, false, true, qerror.CodePermissionDenied},
		{"stale policy", nil, true, false, qerror.CodeUnavailable},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			proc := new(policyProcessor)
			proc.config.Action, proc.config.ResourcePrefix, proc.config.EnforceLabel = "query", "queries", "team_id"
			proc.config.MaxPolicyAge = time.Minute
			proc.domain = "team"
			proc.allows = func(qdata.Identity) bool { return scenario.allowedIdentity }

			proc.check = func(_ context.Context, domain string, roles []string, action, resource string) error {
				require.Equal(t, "team", domain)
				require.Equal(t, []string{"reader"}, roles)
				require.Equal(t, "query", action)
				require.Equal(t, "team:queries.metrics", resource)

				return scenario.checkErr
			}
			if scenario.fresh {
				proc.lastRefresh.Store(time.Now().UnixNano())
			}

			query := &qdata.Query{Signal: qdata.SignalMetrics}
			ctx := qdata.WithIdentity(t.Context(), qdata.Identity{
				Authority: "athenz:team", Principal: "alice", Tenant: "a", Roles: []string{"reader"}, Groups: nil, Scopes: nil,
			})

			err := proc.ProcessQuery(ctx, query)
			if scenario.want != qerror.CodeInternal {
				require.Equal(t, scenario.want, qerror.CodeOf(err))
				require.Empty(t, query.GetEnforcedMatchers())

				return
			}

			require.NoError(t, err)
			require.Len(t, query.GetEnforcedMatchers(), 1)
			require.Equal(t, "a", query.GetEnforcedMatchers()[0].GetValue())
		})
	}
}
