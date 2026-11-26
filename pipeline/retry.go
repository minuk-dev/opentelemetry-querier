package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

var (
	errBackendRetryBounds  = errors.New("backend retry attempts must be 0–10 and backoff must be 0–1m")
	errBackendRetryBackoff = errors.New("backend retries require positive backoff")
)

// RetryConfig controls additional read attempts for one backend.
type RetryConfig struct {
	MaxAttempts int           `mapstructure:"max_attempts"`
	Backoff     time.Duration `mapstructure:"backoff"`
}

// Validate rejects unbounded or invalid retry settings.
func (c RetryConfig) Validate() error {
	if c.MaxAttempts < 0 || c.MaxAttempts > 10 || c.Backoff < 0 || c.Backoff > time.Minute {
		return errBackendRetryBounds
	}

	if c.MaxAttempts > 1 && c.Backoff == 0 {
		return errBackendRetryBackoff
	}

	return nil
}

// WithBackendRetry wraps a read dispatcher while preserving lifecycle calls.
func WithBackendRetry(next dispatcher.Dispatcher, cfg RetryConfig, logger *slog.Logger) dispatcher.Dispatcher {
	if cfg.MaxAttempts <= 1 {
		return next
	}

	return retryDispatcher{Dispatcher: next, cfg: cfg, logger: logger}
}

type retryDispatcher struct {
	dispatcher.Dispatcher

	cfg    RetryConfig
	logger *slog.Logger
}

func (d retryDispatcher) Dispatch(ctx context.Context, query *qdata.Query) (*qdata.Result, error) {
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return nil, queryContextError(ctx)
		}

		result, err := d.Dispatcher.Dispatch(ctx, query)
		if ctx.Err() != nil {
			return nil, queryContextError(ctx)
		}

		if !d.canRetry(query, attempt, err) {
			if attempt > 1 {
				d.logger.DebugContext(ctx, "backend retry finished", "attempts", attempt, "success", err == nil)
			}

			if err != nil {
				return result, fmt.Errorf("backend attempt %d: %w", attempt, err)
			}

			return result, nil
		}

		d.logger.DebugContext(ctx, "backend retry scheduled", "attempt", attempt+1)

		timer := time.NewTimer(d.cfg.Backoff)
		select {
		case <-ctx.Done():
			timer.Stop()

			return nil, queryContextError(ctx)
		case <-timer.C:
		}
	}
}

func (d retryDispatcher) canRetry(query *qdata.Query, attempt int, err error) bool {
	return query.GetContext() != qdata.ContextStreaming && attempt < d.cfg.MaxAttempts && qerror.RetryableRead(err)
}
