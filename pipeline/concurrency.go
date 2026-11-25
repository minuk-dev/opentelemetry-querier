package pipeline

import (
	"context"
	"errors"
	"sync"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// ConcurrencyConfig sets process-local in-flight caps and queue capacity.
type ConcurrencyConfig struct {
	PerTenant int            `mapstructure:"per_tenant"`
	Backends  map[string]int `mapstructure:"backends"`
	QueueSize int            `mapstructure:"queue_size"`
}

// Concurrency admits work to shared tenant and backend slots in this process.
type Concurrency struct {
	cfg     ConcurrencyConfig
	mu      sync.Mutex
	active  map[string]int
	waiting int
	changed chan struct{}
}

type tenantSlotKey struct{}

// NewConcurrency constructs a limiter for the service.
func NewConcurrency(cfg ConcurrencyConfig) (*Concurrency, error) {
	if cfg.PerTenant < 0 || cfg.QueueSize < 0 {
		return nil, errors.New("concurrency caps must be non-negative")
	}
	for _, limit := range cfg.Backends {
		if limit <= 0 {
			return nil, errors.New("configured backend caps must be positive")
		}
	}
	return &Concurrency{cfg: cfg, active: make(map[string]int), changed: make(chan struct{})}, nil
}

func (c *Concurrency) admitTenant(ctx context.Context) (context.Context, func(), error) {
	if c.cfg.PerTenant == 0 || ctx.Value(tenantSlotKey{}) == c {
		return ctx, func() {}, nil
	}
	identity, verified := qdata.IdentityFromContext(ctx)
	if !verified || identity.Tenant == "" {
		return ctx, nil, qerror.New(qerror.CodeUnauthenticated, "concurrency requires a verified tenant")
	}
	release, err := c.acquire(ctx, "tenant:"+identity.Tenant, c.cfg.PerTenant)
	if err != nil {
		return ctx, nil, err
	}
	return context.WithValue(ctx, tenantSlotKey{}, c), release, nil
}

// WrapBackend counts active storage calls, excluding cache hits.
func (c *Concurrency) WrapBackend(id string, next dispatcher.Dispatcher) dispatcher.Dispatcher {
	limit := c.cfg.Backends[id]
	if limit == 0 {
		return next
	}
	return limitedBackend{Dispatcher: next, limiter: c, key: "backend:" + id, limit: limit}
}

type limitedBackend struct {
	dispatcher.Dispatcher
	limiter *Concurrency
	key     string
	limit   int
}

func (b limitedBackend) Dispatch(ctx context.Context, q *qdata.Query) (*qdata.Result, error) {
	release, err := b.limiter.acquire(ctx, b.key, b.limit)
	if err != nil {
		return nil, err
	}
	defer release()
	if ctx.Err() != nil {
		return nil, admissionContextError(ctx)
	}
	return b.Dispatcher.Dispatch(ctx, q)
}

func admissionContextError(ctx context.Context) error {
	if ctx.Err() == context.DeadlineExceeded {
		return qerror.New(qerror.CodeDeadlineExceeded, "query deadline expired during admission")
	}
	return qerror.New(qerror.CodeCanceled, "query canceled during admission")
}

func (c *Concurrency) acquire(ctx context.Context, key string, limit int) (func(), error) {
	queued := false
	for {
		c.mu.Lock()
		if ctx.Err() != nil {
			if queued {
				c.waiting--
			}
			c.mu.Unlock()
			return nil, admissionContextError(ctx)
		}
		if c.active[key] < limit {
			if queued {
				c.waiting--
			}
			c.active[key]++
			c.mu.Unlock()
			return func() {
				c.mu.Lock()
				c.active[key]--
				if c.active[key] == 0 {
					delete(c.active, key)
				}
				close(c.changed)
				c.changed = make(chan struct{})
				c.mu.Unlock()
			}, nil
		}
		if !queued {
			if c.waiting == c.cfg.QueueSize {
				c.mu.Unlock()
				return nil, qerror.New(qerror.CodeResourceExhausted, "query concurrency queue is full")
			}
			c.waiting++
			queued = true
		}
		changed := c.changed
		c.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
		}
	}
}
