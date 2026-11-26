// Package cacheprocessor caches results at a configured pipeline position.
package cacheprocessor

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"text/template"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

var errCacheBounds = errors.New("cache requires positive ttl and max_bytes")

// Config bounds the cache and optionally selects additional key dimensions.
type Config struct {
	RequireVerifiedIdentity bool          `mapstructure:"require_verified_identity"`
	TTL                     time.Duration `mapstructure:"ttl"`
	MaxBytes                int           `mapstructure:"max_bytes"`
	Key                     string        `mapstructure:"key"`
}

// New builds a local cache processor.
func New(cfg Config) (*Processor, error) {
	if cfg.TTL <= 0 || cfg.MaxBytes <= 0 {
		return nil, errCacheBounds
	}

	var keyTemplate *template.Template

	if cfg.Key != "" {
		parsed, err := template.New("cache key").Option("missingkey=error").Parse(cfg.Key)
		if err != nil {
			return nil, fmt.Errorf("parse cache key: %w", err)
		}

		keyTemplate = parsed
	}

	return &Processor{
		Base: processor.Base{}, cfg: cfg, keyTemplate: keyTemplate, mu: sync.Mutex{},
		entries: make(map[cacheKey]*list.Element), lru: list.List{},
		bytes: 0, flights: make(map[cacheKey]*cacheFlight),
	}, nil
}

type cacheKey [sha256.Size]byte

type cacheEntry struct {
	key     cacheKey
	payload []byte
	expires time.Time
}

func (e *cacheEntry) size() int { return sha256.Size + len(e.payload) }

type cacheFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	result  *qdata.Result
	err     error
	entry   *cacheEntry
}

// Processor owns an isolated cache for its position in one pipeline.
type Processor struct {
	processor.Base

	cfg         Config
	keyTemplate *template.Template

	mu      sync.Mutex
	entries map[cacheKey]*list.Element
	lru     list.List
	bytes   int
	flights map[cacheKey]*cacheFlight
}

func cacheEligible(query *qdata.Query) bool {
	if query.GetPlan() == nil || qdata.TenantID(query) == "" ||
		qdata.Metadata(query, qdata.MetadataImplicitEvaluationTime) != "" {
		return false
	}

	mode := query.GetContext()

	return (mode == qdata.ContextInstant || mode == qdata.ContextRange) && query.GetRange().GetEnd() != nil
}

// RequiresTerminalDispatchPosition keeps response filtering outside the verified cache.
func (c *Processor) RequiresTerminalDispatchPosition() bool { return c.cfg.RequireVerifiedIdentity }

// ProcessDispatch serves a cached response or shares a downstream execution.
func (c *Processor) ProcessDispatch(
	ctx context.Context, query *qdata.Query, next processor.DispatchFunc, respond processor.RespondFunc,
) (*qdata.Result, error) {
	if !cacheEligible(query) {
		return runDirect(ctx, query, next, respond)
	}

	identity, verified := qdata.IdentityFromContext(ctx)
	if c.cfg.RequireVerifiedIdentity && !matchesVerifiedTenant(query, identity, verified) {
		return runDirect(ctx, query, next, respond)
	}

	snapshot := proto.CloneOf(query)

	key, err := c.keyWithIdentity(snapshot, identity, verified)
	if err != nil {
		return runDirect(ctx, query, next, respond)
	}

	err = ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("wait for cache result: %w", qerror.UpstreamTransportError(err))
	}

	entry, flight := c.acquire(ctx, key, next, snapshot)
	if entry != nil {
		return serveEntry(ctx, query, entry, next, respond)
	}

	defer c.release(key, flight)

	return c.finishFlight(ctx, key, flight, respond)
}

func matchesVerifiedTenant(query *qdata.Query, identity qdata.Identity, verified bool) bool {
	return verified && identity.Authority != "" && identity.Principal != "" && identity.Tenant == qdata.TenantID(query)
}

func (c *Processor) keyWithIdentity(query *qdata.Query, identity qdata.Identity, verified bool) (cacheKey, error) {
	key, err := c.key(query)
	if err != nil || !verified {
		return key, err
	}

	return verifiedKey(key, identity)
}

func verifiedKey(key cacheKey, identity qdata.Identity) (cacheKey, error) {
	normalize := func(values []string) []string {
		out := append([]string(nil), values...)
		slices.Sort(out)

		return slices.Compact(out)
	}
	identity.Groups = normalize(identity.Groups)
	identity.Roles = normalize(identity.Roles)
	identity.Scopes = normalize(identity.Scopes)

	encoded, err := json.Marshal(identity)
	if err != nil {
		return cacheKey{}, fmt.Errorf("encode verified identity: %w", err)
	}

	hash := sha256.New()
	_, _ = hash.Write(key[:])
	_, _ = hash.Write(encoded)

	return cacheKey(hash.Sum(nil)), nil
}

func runDirect(
	ctx context.Context, query *qdata.Query, next processor.DispatchFunc, respond processor.RespondFunc,
) (*qdata.Result, error) {
	result, err := next(ctx, query)
	if err != nil {
		return nil, err
	}

	return respond(result)
}

func serveEntry(
	ctx context.Context, query *qdata.Query, entry *cacheEntry, next processor.DispatchFunc,
	respond processor.RespondFunc,
) (*qdata.Result, error) {
	result := new(qdata.Result)

	err := proto.Unmarshal(entry.payload, result)
	if err != nil {
		return runDirect(ctx, query, next, respond)
	}

	return respond(result)
}

func (c *Processor) key(query *qdata.Query) (cacheKey, error) {
	var (
		options proto.MarshalOptions
		variant []byte
	)

	options.Deterministic = true

	if c.keyTemplate != nil {
		hasher := sha256.New()

		err := c.keyTemplate.Execute(hasher, query)
		if err != nil {
			return cacheKey{}, fmt.Errorf("render cache key: %w", err)
		}

		variant = hasher.Sum(nil)
	}

	headers := query.GetHeader()
	if c.keyTemplate != nil {
		// A template chooses other header dimensions. Credentials always
		// partition results alongside the rewritten query and metadata.
		credentials := maps.Clone(headers)
		maps.DeleteFunc(credentials, func(name string, _ *qdata.HeaderValues) bool {
			return !strings.EqualFold(name, "Authorization") && !strings.EqualFold(name, "Cookie")
		})
		query.Header = credentials
	}

	encoded, err := options.Marshal(query)
	query.Header = headers

	if err != nil {
		return cacheKey{}, fmt.Errorf("encode cache key: %w", err)
	}

	if variant == nil {
		return sha256.Sum256(encoded), nil
	}

	hasher := sha256.New()
	_, _ = hasher.Write(variant)
	_, _ = hasher.Write(encoded)

	return cacheKey(hasher.Sum(nil)), nil
}

func (c *Processor) finishFlight(
	ctx context.Context, key cacheKey, flight *cacheFlight, respond processor.RespondFunc,
) (*qdata.Result, error) {
	select {
	case <-flight.done:
	case <-ctx.Done():
	}

	err := ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("wait for cache result: %w", qerror.UpstreamTransportError(err))
	}

	if flight.err != nil {
		return nil, flight.err
	}

	result, err := respond(proto.CloneOf(flight.result))
	if err != nil {
		return nil, err
	}

	err = ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("wait for cache result: %w", qerror.UpstreamTransportError(err))
	}

	c.publish(ctx, key, flight)

	return result, nil
}

// acquire checks both maps under the same lock so concurrent misses share the
// dispatch even if the first response is still running its result processors.
func (c *Processor) acquire(
	ctx context.Context, key cacheKey, next processor.DispatchFunc, query *qdata.Query,
) (*cacheEntry, *cacheFlight) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if element := c.entries[key]; element != nil {
		entry, _ := element.Value.(*cacheEntry)
		if time.Now().Before(entry.expires) {
			c.lru.MoveToFront(element)

			return entry, nil
		}

		c.remove(element)
	}

	if flight := c.flights[key]; flight != nil {
		flight.waiters++

		return nil, flight
	}

	// Each caller keeps its own deadline. The shared work lasts until dispatch
	// completes or its last waiter leaves.
	dispatchCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	flight := &cacheFlight{done: make(chan struct{}), cancel: cancel, waiters: 1, result: nil, err: nil, entry: nil}
	c.flights[key] = flight

	go func() {
		defer cancel()

		c.dispatch(dispatchCtx, key, flight, next, query)
	}()

	return nil, flight
}

func (c *Processor) dispatch(
	ctx context.Context, key cacheKey, flight *cacheFlight, next processor.DispatchFunc, query *qdata.Query,
) {
	flight.result, flight.err = next(ctx, query)
	if flight.err != nil {
		c.mu.Lock()

		if c.flights[key] == flight {
			delete(c.flights, key)
		}

		c.mu.Unlock()
	} else if ctx.Err() == nil {
		flight.entry = c.encode(key, flight.result)
	}

	close(flight.done)
}

func (c *Processor) encode(key cacheKey, result *qdata.Result) *cacheEntry {
	metadata := result.GetLogs().GetSearchMetadata()
	if result == nil || metadata.GetTimedOut() || metadata.GetShards().GetFailed() > 0 {
		return nil
	}

	limit := c.cfg.MaxBytes - sha256.Size
	if proto.Size(result) > limit {
		return nil
	}

	payload, err := proto.Marshal(result)
	if err != nil || len(payload) > limit {
		return nil
	}

	return &cacheEntry{key: key, payload: payload, expires: time.Now().Add(c.cfg.TTL)}
}

func (c *Processor) release(key cacheKey, flight *cacheFlight) {
	c.mu.Lock()
	defer c.mu.Unlock()

	flight.waiters--

	if flight.waiters == 0 && c.flights[key] == flight {
		delete(c.flights, key)
		flight.cancel()
	}
}

// publish retains results only once a caller has successfully processed them.
// Other callers still run their own response processors against a private copy.
func (c *Processor) publish(ctx context.Context, key cacheKey, flight *cacheFlight) {
	entry := flight.entry
	if entry == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.flights[key] != flight || ctx.Err() != nil || !time.Now().Before(entry.expires) {
		return
	}

	for entry.size() > c.cfg.MaxBytes-c.bytes {
		c.remove(c.lru.Back())
	}

	c.entries[key] = c.lru.PushFront(entry)
	c.bytes += entry.size()

	delete(c.flights, key)
}

func (c *Processor) remove(element *list.Element) {
	entry, _ := element.Value.(*cacheEntry)
	c.bytes -= entry.size()

	delete(c.entries, entry.key)
	c.lru.Remove(element)
}
