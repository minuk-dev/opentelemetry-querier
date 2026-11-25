package simpleauthzprocessor

import (
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/stretchr/testify/require"
)

func TestVerifiedGroupPolicies(t *testing.T) {
	t.Parallel()
	processor := New(Config{GroupRules: []GroupRule{
		{Groups: []string{"reader"}, Effect: EffectAllow, EnforceLabels: []EnforceLabel{{Name: "team", Value: "a"}}},
		{Groups: []string{"suspended"}, Effect: EffectDeny},
	}})
	for _, tc := range []struct {
		name   string
		groups []string
		allow  bool
	}{
		{"allowed", []string{"reader"}, true},
		{"deny wins", []string{"reader", "suspended"}, false},
		{"missing verified groups", nil, false},
		{"unmatched", []string{"unrelated"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := &qdata.Query{Header: map[string]*qdata.HeaderValues{"X-Querier-Group": {Values: []string{"reader"}}}}
			ctx := qdata.WithIdentity(t.Context(), qdata.Identity{Principal: "alice", Groups: tc.groups})
			err := processor.ProcessQuery(ctx, query)
			if !tc.allow {
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
