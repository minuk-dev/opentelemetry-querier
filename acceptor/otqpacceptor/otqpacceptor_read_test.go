package otqpacceptor //nolint:testpackage // Exercise the production HTTP server settings with a shorter deadline.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

func TestHTTPQueryTimesOutWhileReadingBody(t *testing.T) {
	t.Parallel()

	called := make(chan struct{}, 1)
	acceptor := New(Config{GRPCEndpoint: "", HTTPEndpoint: "127.0.0.1:0"},
		pipeline.HandlerFunc(func(context.Context, *qdata.Query) (*qdata.Result, error) {
			called <- struct{}{}

			return &qdata.Result{}, nil
		}))

	server := httptest.NewUnstartedServer(nil)
	server.Config = acceptor.newHTTPServer()
	require.Positive(t, server.Config.ReadTimeout)
	server.Config.ReadTimeout = 100 * time.Millisecond
	server.Start()
	t.Cleanup(server.Close)

	reader, writer := io.Pipe()

	defer func() { _ = writer.Close() }()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+HTTPQueryPath, reader)
	require.NoError(t, err)

	request.ContentLength = 1
	request.Header.Set("Content-Type", "application/json")

	response, err := server.Client().Do(request)
	require.NoError(t, err)
	assert.Equal(t, http.StatusRequestTimeout, response.StatusCode)
	require.NoError(t, response.Body.Close())
	assert.Empty(t, called)
}
