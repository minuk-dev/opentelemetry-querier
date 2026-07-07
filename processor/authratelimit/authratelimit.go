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
	RequireBearer bool `mapstructure:"require_bearer"`
	// Tokens is the set of accepted bearer tokens.
	Tokens []string `mapstructure:"tokens"`

	// RequestsPerSecond is the sustained per-key query rate; zero disables rate
	// limiting.
	RequestsPerSecond float64 `mapstructure:"requests_per_second"`
	// Burst is the bucket capacity; defaults to ceil(RequestsPerSecond) or 1.
	Burst int `mapstructure:"burst"`
	// PerTenant keys the limiter by tenant id instead of applying one global
	// bucket.
	PerTenant bool `mapstructure:"per_tenant"`
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
	for _, token := range cfg.Tokens {
		tokens[token] = struct{}{}
	}

	var lim *limiter

	if cfg.RequestsPerSecond > 0 {
		burst := cfg.Burst
		if burst <= 0 {
			burst = max(int(cfg.RequestsPerSecond), 1)
		}

		lim = newLimiter(cfg.RequestsPerSecond, float64(burst))
	}

	return &Processor{Base: processor.Base{}, cfg: cfg, tokens: tokens, limiter: lim}
}

// ProcessQuery checks the bearer token then the rate limit.
func (p *Processor) ProcessQuery(_ context.Context, query *qdata.Query) error {
	if p.cfg.RequireBearer && !p.authorized(query) {
		return qerror.New(qerror.CodeUnauthenticated, "authratelimit: missing or invalid bearer token")
	}

	if p.limiter != nil {
		key := "global"
		if p.cfg.PerTenant {
			key = query.GetTenantId()
		}

		if !p.limiter.allow(key) {
			return qerror.New(qerror.CodeResourceExhausted, "authratelimit: rate limit exceeded")
		}
	}

	return nil
}

func (p *Processor) authorized(query *qdata.Query) bool {
	const prefix = "Bearer "

	var raw string

	for key, values := range query.GetHeader() {
		if strings.EqualFold(key, "Authorization") && len(values.GetValues()) > 0 {
			raw = values.GetValues()[0]

			break
		}
	}

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
	return &limiter{rate: rate, burst: burst, mu: sync.Mutex{}, keys: make(map[string]*bucket)}
}

// allow refills the key's bucket by elapsed time and consumes one token.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()

	state := l.keys[key]
	if state == nil {
		state = &bucket{tokens: l.burst, last: now}
		l.keys[key] = state
	}

	state.tokens += now.Sub(state.last).Seconds() * l.rate
	if state.tokens > l.burst {
		state.tokens = l.burst
	}

	state.last = now
	if state.tokens < 1 {
		return false
	}

	state.tokens--

	return true
}
