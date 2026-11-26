package pipeline //nolint:testpackage // Uses queue state to synchronize the cancellation test.

import (
	"context"
	"testing"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/stretchr/testify/require"
)

func TestConcurrencyQueue(t *testing.T) {
	t.Parallel()

	limiter, err := NewConcurrency(ConcurrencyConfig{PerTenant: 0, Backends: nil, QueueSize: 1})
	require.NoError(t, err)
	release, err := limiter.acquire(t.Context(), "backend:a", 1)
	require.NoError(t, err)

	defer release()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)

	go func() {
		release, err := limiter.acquire(ctx, "backend:a", 1)
		if err == nil {
			release()
		}

		done <- err
	}()

	require.Eventually(t, func() bool {
		limiter.mu.Lock()
		defer limiter.mu.Unlock()

		return limiter.waiting == 1
	}, time.Second, time.Millisecond)
	_, err = limiter.acquire(t.Context(), "backend:a", 1)
	require.Equal(t, qerror.CodeResourceExhausted, qerror.CodeOf(err))
	free, err := limiter.acquire(t.Context(), "backend:b", 1)
	require.NoError(t, err)
	free()
	cancel()

	select {
	case err := <-done:
		require.Equal(t, qerror.CodeCanceled, qerror.CodeOf(err))
	case <-time.After(time.Second):
		t.Fatal("queued query did not cancel")
	}
}
