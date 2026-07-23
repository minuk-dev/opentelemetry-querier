package otqpacceptor_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/minuk-dev/opentelemetry-querier/acceptor/otqpacceptor"
	otqpv1 "github.com/minuk-dev/opentelemetry-querier/gen/otqp/v1"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// startGRPC boots an OTQP gRPC acceptor on an ephemeral port with the given
// handler and returns a connected client conn. The query seen by the handler is
// delivered on the provided channel. Cleanup is registered on t.
func startGRPC(t *testing.T, seen chan<- *qdata.Query) *grpc.ClientConn {
	t.Helper()

	handler := pipeline.HandlerFunc(func(_ context.Context, query *qdata.Query) (*qdata.Result, error) {
		seen <- query

		return &qdata.Result{}, nil
	})

	acceptor := otqpacceptor.New(otqpacceptor.Config{GRPCEndpoint: "127.0.0.1:0", HTTPEndpoint: ""}, handler)
	require.NoError(t, acceptor.Start(context.Background(), nil))
	t.Cleanup(func() { _ = acceptor.Shutdown(context.Background()) })

	// GRPCListenAddr resolves the ephemeral port the acceptor actually bound, so
	// the test never has to pick a port and race to re-bind it.
	conn, err := grpc.NewClient(acceptor.GRPCListenAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// waitForQuery returns the query the handler saw, failing if it never arrived.
func waitForQuery(t *testing.T, seen <-chan *qdata.Query) *qdata.Query {
	t.Helper()

	select {
	case query := <-seen:
		return query
	case <-time.After(2 * time.Second):
		t.Fatal("handler was not invoked")

		return nil
	}
}

// TestGRPCInjectsMetadataAsHeaders is the end-to-end regression test for the gRPC
// header gap: a gRPC client sends credentials as metadata, not in the request
// body, so both the unary and the streaming handler must carry that metadata
// onto the query as headers or header-based processors (auth, tenant) see
// nothing over gRPC.
func TestGRPCInjectsMetadataAsHeaders(t *testing.T) {
	t.Parallel()

	// Both RPCs must inject metadata, so the regression runs against each.
	invokers := []struct {
		name string
		call func(t *testing.T, ctx context.Context, client otqpv1.QueryServiceClient, req *otqpv1.QueryRequest)
	}{
		{
			name: "Query",
			call: func(t *testing.T, ctx context.Context, client otqpv1.QueryServiceClient, req *otqpv1.QueryRequest) {
				t.Helper()

				_, err := client.Query(ctx, req)
				require.NoError(t, err)
			},
		},
		{
			name: "QueryStream",
			call: func(t *testing.T, ctx context.Context, client otqpv1.QueryServiceClient, req *otqpv1.QueryRequest) {
				t.Helper()

				stream, err := client.QueryStream(ctx, req)
				require.NoError(t, err)

				_, err = stream.Recv()
				require.NoError(t, err)
			},
		},
	}

	for _, invoker := range invokers {
		t.Run(invoker.name, func(t *testing.T) {
			t.Parallel()

			seen := make(chan *qdata.Query, 1)
			client := otqpv1.NewQueryServiceClient(startGRPC(t, seen))

			// gRPC lower-cases metadata keys; the acceptor must still surface them
			// so a case-insensitive lookup on "X-Scope-User" finds the value.
			ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
				"x-scope-user", "alice",
				"x-scope-orgid", "acme",
			))

			invoker.call(t, ctx, client, &otqpv1.QueryRequest{Query: &qdata.Query{}})

			query := waitForQuery(t, seen)

			require.Contains(t, query.GetHeader(), "x-scope-user")
			assert.Equal(t, []string{"alice"}, query.GetHeader()["x-scope-user"].GetValues())
			require.Contains(t, query.GetHeader(), "x-scope-orgid")
			assert.Equal(t, []string{"acme"}, query.GetHeader()["x-scope-orgid"].GetValues())

			// Reserved gRPC/HTTP2 metadata must not pollute the query. content-type
			// is always sent by the gRPC transport, so its absence proves filtering.
			assert.NotContains(t, query.GetHeader(), "content-type")
			assert.NotContains(t, query.GetHeader(), "user-agent")
		})
	}
}

// TestGRPCMetadataOverridesBodyHeader confirms transport metadata authoritatively
// overrides a header the client also set in the request body, with no
// case-duplicate left behind for a case-insensitive lookup to pick between.
func TestGRPCMetadataOverridesBodyHeader(t *testing.T) {
	t.Parallel()

	seen := make(chan *qdata.Query, 1)
	client := otqpv1.NewQueryServiceClient(startGRPC(t, seen))

	// Body carries a canonical-cased value; metadata carries the lower-cased
	// transport value. Only the metadata value must survive.
	req := &otqpv1.QueryRequest{Query: &qdata.Query{
		Header: map[string]*qdata.HeaderValues{"X-Scope-User": {Values: []string{"from-body"}}},
	}}
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-scope-user", "alice"))

	_, err := client.Query(ctx, req)
	require.NoError(t, err)

	query := waitForQuery(t, seen)

	// Exactly one entry matches "x-scope-user" case-insensitively, and it is the
	// metadata value.
	matches := 0

	for key, values := range query.GetHeader() {
		if key == "x-scope-user" || key == "X-Scope-User" {
			matches++

			assert.Equal(t, []string{"alice"}, values.GetValues())
		}
	}

	assert.Equal(t, 1, matches, "expected a single x-scope-user entry, got header map %v", query.GetHeader())
}

// TestGRPCMetadataIsPerRPC guards the semantics the whole fix relies on: gRPC
// metadata is delivered per RPC (per HTTP/2 stream), not once per connection.
// Three calls reuse the SAME connection; the second still receives its own fresh
// value (not the first's cached one), and a call the client sends without
// metadata carries none. HPACK compresses repeated headers on the wire but the
// server always reconstructs the full per-stream set.
func TestGRPCMetadataIsPerRPC(t *testing.T) {
	t.Parallel()

	seen := make(chan *qdata.Query, 1)
	client := otqpv1.NewQueryServiceClient(startGRPC(t, seen))

	callUser := func(user string) []string {
		ctx := context.Background()
		if user != "" {
			ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("x-scope-user", user))
		}

		_, err := client.Query(ctx, &otqpv1.QueryRequest{Query: &qdata.Query{}})
		require.NoError(t, err)

		return waitForQuery(t, seen).GetHeader()["x-scope-user"].GetValues()
	}

	// All three RPCs share the one connection opened by startGRPC.
	assert.Equal(t, []string{"alice"}, callUser("alice"), "first RPC")
	assert.Equal(t, []string{"bob"}, callUser("bob"), "second RPC on the same connection is not the first's cached value")
	assert.Empty(t, callUser(""), "an RPC the client sends without metadata carries none")
}
