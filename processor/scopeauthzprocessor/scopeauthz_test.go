package scopeauthzprocessor //nolint:testpackage // Checks the internal policy with verified identities.

import (
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/stretchr/testify/require"
)

func TestRequiredScopes(t *testing.T) {
	t.Parallel()

	proc := &scopeProcessor{Base: processor.Base{}, config: Config{
		Issuer: "trusted", RequiredScopes: []string{"query", "read"},
	}}
	require.Equal(t, qerror.CodePermissionDenied, qerror.CodeOf(proc.ProcessQuery(t.Context(), nil)))

	identity := qdata.Identity{
		Authority: "trusted", Principal: "alice", Tenant: "team", Groups: nil, Roles: nil, Scopes: []string{"query"},
	}
	require.Equal(t, qerror.CodePermissionDenied,
		qerror.CodeOf(proc.ProcessQuery(qdata.WithIdentity(t.Context(), identity), nil)))

	identity.Scopes = append(identity.Scopes, "read")
	require.NoError(t, proc.ProcessQuery(qdata.WithIdentity(t.Context(), identity), nil))

	identity.Authority = "other"
	require.Equal(t, qerror.CodePermissionDenied,
		qerror.CodeOf(proc.ProcessQuery(qdata.WithIdentity(t.Context(), identity), nil)))
}
