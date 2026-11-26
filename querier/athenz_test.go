package querier

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AthenZ/athenz-authorizer/v5/policy"
	"github.com/AthenZ/athenz-authorizer/v5/role"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/stretchr/testify/require"
)

func TestAthenzAuthorization(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, domain         string
		verifyErr, policyErr error
		stale                bool
		want                 qerror.Code
	}{
		{"allowed", "team", nil, nil, false, qerror.CodeInternal},
		{"explicit deny", "team", nil, policy.ErrDenyByPolicy, false, qerror.CodePermissionDenied},
		{"unmatched resource", "team", nil, policy.ErrNoMatch, false, qerror.CodePermissionDenied},
		{"wrong domain", "other", nil, nil, false, qerror.CodePermissionDenied},
		{"invalid token", "team", errors.New("invalid"), nil, false, qerror.CodeUnauthenticated},
		{"unavailable policy", "team", nil, nil, true, qerror.CodeUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := &athenzIdentity{cfg: AthenzIdentityConfig{
				Domain: "team", Action: "query", ResourcePrefix: "queries", Tenant: "a", EnforceLabel: "team_id", MaxPolicyAge: time.Minute,
			}, verify: func(string) (*role.Token, error) {
				return &role.Token{Domain: tc.domain, Principal: "alice", Roles: []string{"reader"}}, tc.verifyErr
			}, check: func(_ context.Context, domain string, roles []string, action, resource string) error {
				require.Equal(t, "team", domain)
				require.Equal(t, []string{"reader"}, roles)
				require.Equal(t, "query", action)
				require.Equal(t, "team:queries.metrics", resource)
				return tc.policyErr
			}}
			if !tc.stale {
				auth.lastRefresh.Store(time.Now().UnixNano())
			}
			query := &qdata.Query{Signal: qdata.SignalMetrics, Header: map[string]*qdata.HeaderValues{"Athenz-Role-Auth": {Values: []string{"role-token"}}}}
			principal, err := auth.authenticate(t.Context(), query)
			if tc.want != qerror.CodeInternal {
				require.Equal(t, tc.want, qerror.CodeOf(err))
				require.Empty(t, query.GetEnforcedMatchers())
				return
			}
			require.NoError(t, err)
			require.Equal(t, "alice", principal.Principal)
			require.Equal(t, []string{"a"}, principal.Tenants)
			require.Equal(t, "a", query.GetEnforcedMatchers()[0].GetValue())
		})
	}
}
