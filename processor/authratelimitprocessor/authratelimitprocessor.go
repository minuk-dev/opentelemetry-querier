// Package authratelimitprocessor implements the gateway processor: bearer-token
// authentication and per-tenant rate limiting. It runs first on the request
// path and short-circuits with a coded error (Unauthenticated / Resource
// Exhausted) so no unauthenticated or over-quota query reaches storage.
//
// Per-tenant keys are request-derived, so the limiter holds at most max_keys
// buckets and gates the creation of any further bucket on a shared admission
// bucket; see the package README for what that does and does not guarantee.
package authratelimitprocessor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
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
	// MaxKeys caps how many distinct rate-limit keys hold their own bucket. Once
	// the cap is reached, serving an unseen key costs a token from a shared
	// admission bucket and evicts the least recently used key, so a caller
	// cycling tenant ids can neither grow the map without bound nor buy itself
	// more than the configured rate. Defaults to defaultMaxKeys.
	MaxKeys int `mapstructure:"max_keys"`
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
			burst = max(int(math.Ceil(cfg.RequestsPerSecond)), 1)
		}

		maxKeys := cfg.MaxKeys
		if maxKeys <= 0 {
			maxKeys = defaultMaxKeys
		}

		lim = newLimiter(cfg.RequestsPerSecond, float64(burst), maxKeys)
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
			key = qdata.TenantID(query)
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

const (
	// defaultMaxKeys bounds the bucket map when max_keys is unset. The keys are
	// request-derived (tenant ids), so without a cap the map is an unbounded
	// allocation driven by whoever is calling.
	defaultMaxKeys = 10_000

	// maxKeyBytes caps the length of one key. Nothing upstream bounds a tenant
	// id — it is a header value, so Go's 1 MiB header limit is the only ceiling
	// — and maxKeys alone caps the entry count, not the bytes each entry
	// retains. A longer key is replaced by its digest, which keeps distinct ids
	// distinct without keeping the id itself alive.
	maxKeyBytes = 256
)

// limiter is a token-bucket rate limiter over a bounded key space. It holds at
// most maxKeys buckets, ordered by recency, and gates the creation of any
// further bucket on a shared admission bucket.
type limiter struct {
	rate    float64 // tokens per second
	burst   float64
	maxKeys int

	mu   sync.Mutex
	keys map[string]*entry
	// head/tail order the entries most-recently-used first, so the entry
	// evicted to make room is always the one idle longest.
	head *entry
	tail *entry
	// admission gates minting a bucket for an unseen key once the map is full.
	// Eviction alone would not be enough: re-creating an evicted key hands it a
	// fresh full bucket, so without this a caller cycling tenant ids would still
	// be limited only by how fast the map turns over. Charging every mint to one
	// shared bucket caps that churn at the configured rate.
	admission *bucket
}

// entry is one key's bucket, linked into the limiter's recency list.
type entry struct {
	key    string
	bucket bucket
	prev   *entry
	next   *entry
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(rate, burst float64, maxKeys int) *limiter {
	return &limiter{
		rate:      rate,
		burst:     burst,
		maxKeys:   maxKeys,
		mu:        sync.Mutex{},
		keys:      make(map[string]*entry),
		head:      nil,
		tail:      nil,
		admission: &bucket{tokens: burst, last: time.Now()},
	}
}

// allow refills the key's bucket by elapsed time and consumes one token. A key
// already holding a bucket is limited by that bucket alone; an unseen key
// arriving once the map is full must first take a token from the shared
// admission bucket, and evicts the least recently used key to make room.
func (l *limiter) allow(key string) bool {
	key = boundKey(key)

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()

	if item, ok := l.keys[key]; ok {
		// Touch before taking: a rejected request is still activity, and
		// dropping the key of a tenant being limited would hand it a fresh
		// bucket on its next call.
		l.touch(item)

		return item.bucket.take(now, l.rate, l.burst)
	}

	if len(l.keys) >= l.maxKeys {
		if !l.admission.take(now, l.rate, l.burst) {
			return false
		}

		l.evictOldest()
	}

	item := &entry{key: key, bucket: bucket{tokens: l.burst, last: now}, prev: nil, next: nil}
	l.keys[key] = item
	l.pushFront(item)

	return item.bucket.take(now, l.rate, l.burst)
}

// boundKey replaces an over-long key with its digest, so one entry cannot retain
// an arbitrary number of bytes. Distinct ids stay distinct: the digest is
// collision-resistant, so a caller cannot pick a long id that shares a bucket
// with another tenant.
func boundKey(key string) string {
	if len(key) <= maxKeyBytes {
		return key
	}

	digest := sha256.Sum256([]byte(key))

	return hex.EncodeToString(digest[:])
}

// evictOldest drops the least recently used entry, freeing one slot.
func (l *limiter) evictOldest() {
	oldest := l.tail
	if oldest == nil {
		return
	}

	l.unlink(oldest)
	delete(l.keys, oldest.key)
}

// touch moves an entry to the front of the recency list.
func (l *limiter) touch(item *entry) {
	if l.head == item {
		return
	}

	l.unlink(item)
	l.pushFront(item)
}

func (l *limiter) pushFront(item *entry) {
	item.prev = nil
	item.next = l.head

	if l.head != nil {
		l.head.prev = item
	}

	l.head = item

	if l.tail == nil {
		l.tail = item
	}
}

func (l *limiter) unlink(item *entry) {
	if item.prev != nil {
		item.prev.next = item.next
	} else if l.head == item {
		l.head = item.next
	}

	if item.next != nil {
		item.next.prev = item.prev
	} else if l.tail == item {
		l.tail = item.prev
	}

	item.prev = nil
	item.next = nil
}

// take refills the bucket by elapsed time and consumes one token.
func (b *bucket) take(now time.Time, rate, burst float64) bool {
	b.tokens += now.Sub(b.last).Seconds() * rate
	if b.tokens > burst {
		b.tokens = burst
	}

	b.last = now

	if b.tokens < 1 {
		return false
	}

	b.tokens--

	return true
}
