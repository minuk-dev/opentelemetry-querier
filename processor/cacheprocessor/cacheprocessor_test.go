package cacheprocessor

import (
	"context"
	"testing"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCachePartitionsVerifiedIdentity(t *testing.T) {
	t.Parallel()
	cache, err := New(Config{TTL: time.Minute, MaxBytes: 1024, RequireVerifiedIdentity: true})
	require.NoError(t, err)
	require.Error(t, processor.ValidateOrder([]processor.Processor{cache, processor.Base{}}))
	query := &qdata.Query{Plan: qdata.Plan(qdata.SelectNode(qdata.SignalMetrics, nil)), Range: &qdata.TimeRange{End: timestamppb.Now()}}
	qdata.SetTenantID(query, "a")
	calls := 0
	next := func(context.Context, *qdata.Query) (*qdata.Result, error) {
		calls++
		return &qdata.Result{Signal: qdata.SignalMetrics}, nil
	}
	responses := 0
	respond := func(r *qdata.Result) (*qdata.Result, error) {
		responses++
		require.Equal(t, qdata.SignalMetrics, r.GetSignal())
		r.Signal = qdata.SignalLogs
		return r, nil
	}
	for _, ctx := range []context.Context{
		t.Context(), t.Context(),
		qdata.WithIdentity(t.Context(), qdata.Identity{Authority: "idp", Principal: "alice", Tenant: "a", Groups: []string{"reader", "audit"}}),
		qdata.WithIdentity(t.Context(), qdata.Identity{Authority: "idp", Principal: "alice", Tenant: "a", Groups: []string{"audit", "reader"}}),
		qdata.WithIdentity(t.Context(), qdata.Identity{Authority: "idp", Principal: "alice", Tenant: "a", Groups: []string{"audit"}}),
	} {
		_, err := cache.ProcessDispatch(ctx, query, next, respond)
		require.NoError(t, err)
	}
	require.Equal(t, 4, calls)
	require.Equal(t, 5, responses)
}
