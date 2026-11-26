package pipeline_test

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/stretchr/testify/require"
)

func TestBackendRetry(t *testing.T) {
	t.Parallel()

	for _, scenario := range []struct {
		name         string
		status, want int
		recover      bool
	}{
		{"transient then success", http.StatusServiceUnavailable, 2, true},
		{"permanent", http.StatusUnauthorized, 1, false},
		{"attempts exhausted", http.StatusBadGateway, 3, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			backend := deadlineDispatcher{Base: dispatcher.Base{}, run: func(context.Context) (*qdata.Result, error) {
				calls++
				if scenario.recover && calls == 2 {
					return &qdata.Result{}, nil
				}

				return nil, qerror.UpstreamHTTPError(scenario.status, nil)
			}}
			retry := pipeline.WithBackendRetry(
				backend, pipeline.RetryConfig{MaxAttempts: 3, Backoff: time.Nanosecond}, slog.Default(),
			)
			_, err := retry.Dispatch(t.Context(), &qdata.Query{})
			require.Equal(t, scenario.want, calls)

			if scenario.recover {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestBackendRetryStopsDuringBackoff(t *testing.T) {
	t.Parallel()

	called := make(chan struct{}, 1)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	backend := deadlineDispatcher{Base: dispatcher.Base{}, run: func(context.Context) (*qdata.Result, error) {
		called <- struct{}{}

		return nil, qerror.UpstreamHTTPError(http.StatusServiceUnavailable, nil)
	}}
	retry := pipeline.WithBackendRetry(backend, pipeline.RetryConfig{MaxAttempts: 3, Backoff: time.Minute}, slog.Default())
	done := make(chan error, 1)

	go func() { _, err := retry.Dispatch(ctx, &qdata.Query{}); done <- err }()

	<-called
	cancel()

	select {
	case err := <-done:
		require.Equal(t, qerror.CodeCanceled, qerror.CodeOf(err))
	case <-time.After(time.Second):
		t.Fatal("retry did not stop")
	}

	require.Empty(t, called)
}
