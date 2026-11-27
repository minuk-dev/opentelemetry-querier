package simpleauthzprocessor_test

import (
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/processor/simpleauthzprocessor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/stretchr/testify/require"
)

func TestVerifiedGroupPolicies(t *testing.T) {
	t.Parallel()

	var cfg simpleauthzprocessor.Config

	cfg.GroupRules = []simpleauthzprocessor.GroupRule{
		{
			Groups: []string{"reader"}, Effect: simpleauthzprocessor.EffectAllow,
			EnforceLabels: []simpleauthzprocessor.EnforceLabel{{Name: "team", Value: "a", FromTenant: false}},
		},
		{Groups: []string{"suspended"}, Effect: simpleauthzprocessor.EffectDeny, EnforceLabels: nil},
	}
	processor := simpleauthzprocessor.New(cfg)

	for _, scenario := range []struct {
		name   string
		groups []string
		allow  bool
	}{
		{"allowed", []string{"reader"}, true},
		{"deny wins", []string{"reader", "suspended"}, false},
		{"missing verified groups", nil, false},
		{"unmatched", []string{"unrelated"}, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			query := &qdata.Query{Header: map[string]*qdata.HeaderValues{"X-Querier-Group": {Values: []string{"reader"}}}}
			ctx := qdata.WithIdentity(t.Context(), qdata.Identity{
				Authority: "idp", Principal: "alice", Tenant: "", Groups: scenario.groups, Roles: nil, Scopes: nil,
			})

			err := processor.ProcessQuery(ctx, query)
			if !scenario.allow {
				require.Equal(t, qerror.CodePermissionDenied, qerror.CodeOf(err))
				require.Empty(t, query.GetEnforcedMatchers())

				return
			}

			require.NoError(t, err)
			require.Len(t, query.GetEnforcedMatchers(), 1)
			require.Equal(t, "a", query.GetEnforcedMatchers()[0].GetValue())
		})
	}
}
