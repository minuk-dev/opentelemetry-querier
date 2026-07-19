package otqp_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/minuk-dev/opentelemetry-querier/acceptor/otqp"
	otqpv1 "github.com/minuk-dev/opentelemetry-querier/gen/otqp/v1"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// freePort binds an ephemeral port, then releases it so the acceptor can claim
// it. Start does not expose its bound address, so the test picks the port.
func freePort(t *testing.T) string {
	t.Helper()

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	return addr
}

// TestGRPCQueryInjectsMetadataAsHeaders is the end-to-end regression test for the
// gRPC header gap: a gRPC client sends credentials as metadata, not in the
// request body, so the acceptor must carry that metadata onto the query as
// headers or header-based processors (auth, tenant, simpleauthz) see nothing
// over gRPC.
func TestGRPCQueryInjectsMetadataAsHeaders(t *testing.T) {
	t.Parallel()

	seen := make(chan *qdata.Query, 1)
	handler := pipeline.HandlerFunc(func(_ context.Context, query *qdata.Query) (*qdata.Result, error) {
		seen <- query

		return &qdata.Result{}, nil
	})

	addr := freePort(t)
	acceptor := otqp.New(otqp.Config{GRPCEndpoint: addr, HTTPEndpoint: ""}, handler)

	require.NoError(t, acceptor.Start(context.Background(), nil))
	t.Cleanup(func() { _ = acceptor.Shutdown(context.Background()) })

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	client := otqpv1.NewQueryServiceClient(conn)

	// gRPC lower-cases metadata keys; the acceptor must still surface them so a
	// case-insensitive lookup on "X-Scope-User" finds the value.
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		"x-scope-user", "alice",
		"x-scope-orgid", "acme",
	))

	_, err = client.Query(ctx, &otqpv1.QueryRequest{Query: &qdata.Query{Expr: "up"}})
	require.NoError(t, err)

	var query *qdata.Query
	select {
	case query = <-seen:
	case <-time.After(2 * time.Second):
		t.Fatal("handler was not invoked")
	}

	require.Contains(t, query.GetHeader(), "x-scope-user")
	assert.Equal(t, []string{"alice"}, query.GetHeader()["x-scope-user"].GetValues())
	require.Contains(t, query.GetHeader(), "x-scope-orgid")
	assert.Equal(t, []string{"acme"}, query.GetHeader()["x-scope-orgid"].GetValues())
}
