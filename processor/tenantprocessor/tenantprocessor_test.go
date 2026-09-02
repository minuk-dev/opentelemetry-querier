package tenantprocessor_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/processor/tenantprocessor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// header builds a Query carrying a single tenant header value.
func header(name, value string) *qdata.Query {
	return &qdata.Query{Header: map[string]*qdata.HeaderValues{name: {Values: []string{value}}}}
}

// TestHeaderBeatsMetadata is the defence-in-depth half of issue #65: the
// acceptors strip client-supplied metadata at ingress, and the processor
// additionally never reads a tenant id from it. The header — the only source an
// upstream gateway controls — decides, and the pre-set id is overwritten rather
// than merged, so the enforced matcher isolates the real tenant.
func TestHeaderBeatsMetadata(t *testing.T) {
	t.Parallel()

	proc := tenantprocessor.New(tenantprocessor.Config{
		Header: tenantprocessor.DefaultHeader, Default: "", Required: true, EnforceLabel: "tenant",
	})

	query := header(tenantprocessor.DefaultHeader, "acme")
	query.Metadata = map[string]string{qdata.MetadataTenantID: "victim-corp"}

	require.NoError(t, proc.ProcessQuery(context.Background(), query))

	assert.Equal(t, "acme", qdata.TenantID(query), "the header resolves the tenant, not the request body")
	require.Len(t, query.GetEnforcedMatchers(), 1)
	assert.Equal(t, "acme", query.GetEnforcedMatchers()[0].GetValue(),
		"the isolation matcher must be built with the header's tenant")
}

// TestUnresolvedClearsMetadata covers the same spoof when no header resolves:
// leaving the client's id behind would hand it to every downstream from_tenant
// consumer and to the dispatchers.
func TestUnresolvedClearsMetadata(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		required bool
		assert   func(t *testing.T, err error)
	}{
		{
			name:     "required rejects",
			required: true,
			assert: func(t *testing.T, err error) {
				t.Helper()
				assert.Equal(t, qerror.CodeUnauthenticated, qerror.CodeOf(err))
			},
		},
		{
			name:     "optional passes through",
			required: false,
			assert: func(t *testing.T, err error) {
				t.Helper()
				require.NoError(t, err)
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			proc := tenantprocessor.New(tenantprocessor.Config{
				Header: tenantprocessor.DefaultHeader, Default: "", Required: testCase.required, EnforceLabel: "tenant",
			})
			query := &qdata.Query{Metadata: map[string]string{qdata.MetadataTenantID: "victim-corp"}}

			testCase.assert(t, proc.ProcessQuery(context.Background(), query))

			assert.Empty(t, qdata.TenantID(query), "an unresolved tenant must not fall back to the client's id")
			assert.Empty(t, query.GetEnforcedMatchers())
		})
	}
}

// TestDefaultAndEnforceLabel pins the ordinary resolution path: the header wins,
// the configured default fills in for its absence, and enforce_label registers
// the isolation matcher.
func TestDefaultAndEnforceLabel(t *testing.T) {
	t.Parallel()

	proc := tenantprocessor.New(tenantprocessor.Config{
		Header: "", Default: "anonymous", Required: true, EnforceLabel: "tenant_id",
	})

	t.Run("header", func(t *testing.T) {
		t.Parallel()

		// The lookup is case-insensitive, so gRPC's lower-cased metadata keys match.
		query := header("x-scope-orgid", "acme")
		require.NoError(t, proc.ProcessQuery(context.Background(), query))
		assert.Equal(t, "acme", qdata.TenantID(query))
	})

	t.Run("default", func(t *testing.T) {
		t.Parallel()

		query := &qdata.Query{}
		require.NoError(t, proc.ProcessQuery(context.Background(), query))
		assert.Equal(t, "anonymous", qdata.TenantID(query))
		require.Len(t, query.GetEnforcedMatchers(), 1)
		assert.Equal(t, "tenant_id", query.GetEnforcedMatchers()[0].GetName())
	})
}
