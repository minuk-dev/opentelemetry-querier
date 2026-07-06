// Package authratelimit implements the gateway processor: bearer-token
// authentication and per-tenant rate limiting. It runs first on the request
// path and short-circuits with a coded error (Unauthenticated / Resource
// Exhausted) so no unauthenticated or over-quota query reaches storage.
package authratelimit

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// Config configures auth and rate limiting.
type Config struct {
	// RequireBearer enables Authorization: Bearer <token> checking.
	RequireBearer bool `yaml:"require_bearer"`
	// Tokens is the set of accepted bearer tokens.
	Tokens []string `yaml:"tokens"`

	// RequestsPerSecond is the sustained per-key query rate; zero disables rate
	// limiting.
	RequestsPerSecond float64 `yaml:"requests_per_second"`
	// Burst is the bucket capacity; defaults to ceil(RequestsPerSecond) or 1.
	Burst int `yaml:"burst"`
	// PerTenant keys the limiter by tenant id instead of applying one global
	// bucket.
	PerTenant bool `yaml:"per_tenant"`
}

// Processor authenticates and rate-limits queries.
type Processor struct {
	processor.Base
	cfg     Config
	tokens  map[string]struct{}
	limiter *limiter
}

// New builds the processor.
func New(cfg Config) *Processor {
	tokens := make(map[string]struct{}, len(cfg.Tokens))
	for _, t := range cfg.Tokens {
		tokens[t] = struct{}{}
	}
	var lim *limiter
	if cfg.RequestsPerSecond > 0 {
		burst := cfg.Burst
		if burst <= 0 {
			burst = int(cfg.RequestsPerSecond)
			if burst < 1 {
				burst = 1
			}
		}
		lim = newLimiter(cfg.RequestsPerSecond, float64(burst))
	}
	return &Processor{cfg: cfg, tokens: tokens, limiter: lim}
}

func (p *Processor) Name() string { return "authratelimit" }

// ProcessQuery checks the bearer token then the rate limit.
func (p *Processor) ProcessQuery(_ context.Context, q *qdata.Query) error {
	if p.cfg.RequireBearer {
		if !p.authorized(q) {
			return qerror.New(qerror.CodeUnauthenticated, "authratelimit: missing or invalid bearer token")
		}
	}
	if p.limiter != nil {
		key := "global"
		if p.cfg.PerTenant {
			key = q.GetTenantId()
		}
		if !p.limiter.allow(key) {
			return qerror.New(qerror.CodeResourceExhausted, "authratelimit: rate limit exceeded")
		}
	}
	return nil
}

func (p *Processor) authorized(q *qdata.Query) bool {
	var raw string
	for k, v := range q.GetHeader() {
		if strings.EqualFold(k, "Authorization") && len(v.GetValues()) > 0 {
			raw = v.GetValues()[0]
			break
		}
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(raw, prefix) {
		return false
	}
	_, ok := p.tokens[strings.TrimPrefix(raw, prefix)]
	return ok
}

// ---- token-bucket limiter ----

type limiter struct {
	rate  float64 // tokens per second
	burst float64
	mu    sync.Mutex
	keys  map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(rate, burst float64) *limiter {
	return &limiter{rate: rate, burst: burst, keys: make(map[string]*bucket)}
}

// allow refills the key's bucket by elapsed time and consumes one token.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b := l.keys[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.keys[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
