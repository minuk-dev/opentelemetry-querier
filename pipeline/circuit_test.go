package pipeline //nolint:testpackage // Sets a fake clock to exercise exact probe transitions.

import (
	"context"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/stretchr/testify/require"
)

type circuitStub struct {
	dispatcher.Base

	run func() error
}

func (d circuitStub) Dispatch(context.Context, *qdata.Query) (*qdata.Result, error) {
	return &qdata.Result{}, d.run()
}

func TestCircuitStateTransitions(t *testing.T) {
	t.Parallel()

	now := time.Now()
	breaker := NewCircuitBreaker(CircuitConfig{FailureThreshold: 2, OpenFor: time.Minute, ProbeLimit: 1}, slog.Default())
	breaker.now = func() time.Time { return now }

	var backendErr error = qerror.New(qerror.CodeUnavailable, "backend down")

	calls := 0

	backend := breaker.Wrap(circuitStub{Base: dispatcher.Base{}, run: func() error {
		calls++

		if backendErr != nil {
			return fmt.Errorf("stub backend: %w", backendErr)
		}

		return nil
	}})
	for range 3 {
		_, err := backend.Dispatch(t.Context(), &qdata.Query{})
		require.Equal(t, qerror.CodeUnavailable, qerror.CodeOf(err))
	}

	require.Equal(t, 2, calls)

	now = now.Add(time.Minute)
	epoch, allowed := breaker.admit()
	require.True(t, allowed)
	_, allowed = breaker.admit()
	require.False(t, allowed)
	breaker.record(epoch, backendErr, false)
	_, allowed = breaker.admit()
	require.False(t, allowed)

	now = now.Add(time.Minute)
	backendErr = nil

	for range 2 {
		_, err := backend.Dispatch(t.Context(), &qdata.Query{})
		require.NoError(t, err)
	}

	require.Equal(t, 4, calls)
}
