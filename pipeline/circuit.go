package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

var errCircuitConfig = errors.New("invalid backend circuit breaker settings")

// CircuitConfig controls a dispatcher ID's circuit breaker.
type CircuitConfig struct {
	FailureThreshold int           `mapstructure:"failure_threshold"`
	OpenFor          time.Duration `mapstructure:"open_for"`
	ProbeLimit       int           `mapstructure:"probe_limit"`
}

// Validate rejects invalid circuit breaker thresholds and durations.
func (c CircuitConfig) Validate() error {
	if c.FailureThreshold < 0 || c.OpenFor < 0 || c.ProbeLimit < 0 || (c.FailureThreshold > 0 && c.OpenFor == 0) {
		return errCircuitConfig
	}

	return nil
}

type circuitState int

const (
	circuitClosed circuitState = iota
	circuitOpen
	circuitHalfOpen
)

// CircuitBreaker is shared by pipelines using the same backend ID.
type CircuitBreaker struct {
	cfg        CircuitConfig
	logger     *slog.Logger
	now        func() time.Time
	mu         sync.Mutex
	state      circuitState
	generation uint64
	failures   int
	probes     int
	reopenAt   time.Time
}

// NewCircuitBreaker constructs a breaker with one recovery probe by default.
func NewCircuitBreaker(config CircuitConfig, logger *slog.Logger) *CircuitBreaker {
	if config.ProbeLimit == 0 {
		config.ProbeLimit = 1
	}

	return &CircuitBreaker{
		cfg: config, logger: logger, now: time.Now, mu: sync.Mutex{}, state: circuitClosed,
		generation: 0, failures: 0, probes: 0, reopenAt: time.Time{},
	}
}

// Wrap adds the circuit policy around a backend's full logical dispatch.
func (b *CircuitBreaker) Wrap(next dispatcher.Dispatcher) dispatcher.Dispatcher {
	return circuitDispatcher{Dispatcher: next, breaker: b}
}

type circuitDispatcher struct {
	dispatcher.Dispatcher

	breaker *CircuitBreaker
}

func (d circuitDispatcher) Dispatch(ctx context.Context, query *qdata.Query) (*qdata.Result, error) {
	if ctx.Err() != nil {
		return nil, queryContextError(ctx)
	}

	epoch, allowed := d.breaker.admit()
	if !allowed {
		return nil, qerror.New(qerror.CodeUnavailable, "backend circuit is open")
	}

	result, err := d.Dispatcher.Dispatch(ctx, query)
	d.breaker.record(epoch, err, ctx.Err() != nil)

	if err != nil {
		return result, fmt.Errorf("dispatch through circuit breaker: %w", err)
	}

	return result, nil
}

func (b *CircuitBreaker) admit() (uint64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == circuitOpen {
		if b.now().Before(b.reopenAt) {
			return 0, false
		}

		b.state = circuitHalfOpen
		b.logger.Debug("backend circuit half-open")
	}

	if b.state == circuitHalfOpen {
		if b.probes >= b.cfg.ProbeLimit {
			return 0, false
		}

		b.probes++
		b.logger.Debug("backend recovery probe admitted", "in_flight", b.probes)
	}

	return b.generation, true
}

func (b *CircuitBreaker) record(epoch uint64, err error, canceled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if epoch != b.generation {
		return
	}

	unhealthy := !canceled && unhealthyBackend(err)
	successful := !canceled && err == nil

	if b.state == circuitHalfOpen {
		b.probes--

		switch {
		case unhealthy:
			b.open()
		case successful:
			b.state = circuitClosed
			b.generation++
			b.failures = 0
			b.probes = 0
			b.logger.Debug("backend circuit closed")
		}

		return
	}

	switch {
	case unhealthy:
		b.failures++
		if b.failures >= b.cfg.FailureThreshold {
			b.open()
		}
	case successful:
		b.failures = 0
	}
}

func (b *CircuitBreaker) open() {
	b.state = circuitOpen
	b.generation++
	b.probes = 0
	b.reopenAt = b.now().Add(b.cfg.OpenFor)
	b.logger.Debug("backend circuit opened", "failure_count", b.failures)
}

func unhealthyBackend(err error) bool {
	code := qerror.CodeOf(err)

	return code == qerror.CodeUnavailable || code == qerror.CodeDeadlineExceeded
}
