package pipeline_test

import (
	"context"
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/stretchr/testify/require"
)

type identityBackend struct {
	dispatcher.Base
	seen qdata.Identity
}

func (d *identityBackend) Dispatch(ctx context.Context, query *qdata.Query) (*qdata.Result, error) {
	d.seen, _ = qdata.IdentityFromContext(ctx)
	if qdata.TenantID(query) != d.seen.Tenant {
		panic("scope differs")
	}
	return &qdata.Result{}, nil
}

func TestVerifiedTenantSelection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		tenants  []string
		selected string
		want     qerror.Code
	}{
		{"single tenant default", []string{"a"}, "", qerror.CodeInternal},
		{"scoped choice", []string{"a", "b"}, "a", qerror.CodeInternal},
		{"missing selection", []string{"a", "b"}, "", qerror.CodePermissionDenied},
		{"outside scope", []string{"b"}, "a", qerror.CodePermissionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &identityBackend{}
			pipe := pipeline.New("test", nil, backend)
			pipe.Authenticate = func(context.Context, *qdata.Query) (pipeline.Principal, error) {
				return pipeline.Principal{Identity: qdata.Identity{Authority: "idp", Principal: "alice"}, Tenants: tc.tenants}, nil
			}
			query := &qdata.Query{}
			if tc.selected != "" {
				query.Header = map[string]*qdata.HeaderValues{"X-Scope-OrgID": {Values: []string{tc.selected}}}
			}
			qdata.SetTenantID(query, "client")
			_, err := pipe.Handle(t.Context(), query)
			if tc.want == qerror.CodePermissionDenied {
				require.Error(t, err)
				require.Equal(t, tc.want, qerror.CodeOf(err))
				require.Empty(t, backend.seen.Principal)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "alice", backend.seen.Principal)
			require.Equal(t, "a", backend.seen.Tenant)
		})
	}
}
