package pipeline_test

import (
	"context"
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
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

func TestVerifiedTenantReachesBackend(t *testing.T) {
	t.Parallel()

	var backend identityBackend

	pipe := pipeline.New("test", nil, &backend)
	identity := qdata.Identity{Authority: "idp", Principal: "alice", Tenant: "a", Groups: nil, Roles: nil, Scopes: nil}
	query := new(qdata.Query)
	qdata.SetTenantID(query, "client")
	_, err := pipe.Handle(qdata.WithIdentity(t.Context(), identity), query)
	require.NoError(t, err)
	require.Equal(t, identity, backend.seen)
}
