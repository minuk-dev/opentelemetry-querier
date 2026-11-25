package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/stretchr/testify/require"
)

func TestConcurrencyQueue(t *testing.T) {
	t.Parallel()
	c, err := NewConcurrency(ConcurrencyConfig{QueueSize: 1})
	require.NoError(t, err)
	release, err := c.acquire(t.Context(), "backend:a", 1)
	require.NoError(t, err)
	defer release()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		release, err := c.acquire(ctx, "backend:a", 1)
		if err == nil {
			release()
		}
		done <- err
	}()
	require.Eventually(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.waiting == 1
	}, time.Second, time.Millisecond)
	_, err = c.acquire(t.Context(), "backend:a", 1)
	require.Equal(t, qerror.CodeResourceExhausted, qerror.CodeOf(err))
	free, err := c.acquire(t.Context(), "backend:b", 1)
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
