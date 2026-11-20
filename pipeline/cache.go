package pipeline

import (
	"container/list"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

var (
	errCacheBounds     = errors.New("query cache requires positive ttl and max_bytes")
	errCacheDispatcher = errors.New("dispatcher does not support query caching")
)

// CacheConfig bounds the lifetime and encoded size of a pipeline's cache.
type CacheConfig struct {
	TTL      time.Duration `mapstructure:"ttl"`
	MaxBytes int           `mapstructure:"max_bytes"`
}

// EnableCache configures a local result cache before the pipeline starts.
func (p *Pipeline) EnableCache(cfg CacheConfig) error {
	if cfg.TTL <= 0 || cfg.MaxBytes <= 0 {
		return errCacheBounds
	}

	cacheable, ok := p.Dispatcher.(dispatcher.CacheableDispatcher)
	if !ok || !cacheable.SupportsQueryCache() {
		return errCacheDispatcher
	}

	p.cache = &queryCache{
		cfg: cfg, mu: sync.Mutex{}, entries: make(map[cacheKey]*list.Element),
		lru: list.List{}, bytes: 0, flights: make(map[cacheKey]*cacheFlight),
	}

	return nil
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

type queryCache struct {
	cfg CacheConfig

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

func (p *Pipeline) handleCached(ctx context.Context, query *qdata.Query) (*qdata.Result, error) {
	snapshot := proto.CloneOf(query)
	// Request processors have already resolved identity and applied policy.
	// Opted-in dispatchers use those outputs and ignore transport headers.
	snapshot.Header = nil

	var options proto.MarshalOptions

	options.Deterministic = true

	keyData, err := options.Marshal(snapshot)
	if err != nil {
		return p.dispatch(ctx, query)
	}

	err = ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("pipeline %q: %w", p.Name, qerror.UpstreamTransportError(err))
	}

	key := cacheKey(sha256.Sum256(keyData))

	entry, flight := p.cache.acquire(ctx, key, p.Dispatcher, snapshot)
	if entry != nil {
		result := new(qdata.Result)

		err := proto.Unmarshal(entry.payload, result)
		if err != nil {
			return p.dispatch(ctx, query)
		}

		return p.processResult(ctx, query, result)
	}

	defer p.cache.release(key, flight)

	return p.finishFlight(ctx, query, key, flight)
}

func (p *Pipeline) finishFlight(
	ctx context.Context, query *qdata.Query, key cacheKey, flight *cacheFlight,
) (*qdata.Result, error) {
	select {
	case <-flight.done:
	case <-ctx.Done():
	}

	err := ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("pipeline %q: %w", p.Name, qerror.UpstreamTransportError(err))
	}

	if flight.err != nil {
		return nil, fmt.Errorf("pipeline %q: %w", p.Name, flight.err)
	}

	result, err := p.processResult(ctx, query, proto.CloneOf(flight.result))
	if err != nil {
		return nil, err
	}

	err = ctx.Err()
	if err != nil {
		return nil, fmt.Errorf("pipeline %q: %w", p.Name, qerror.UpstreamTransportError(err))
	}

	p.cache.publish(ctx, key, flight)

	return result, nil
}

// acquire checks both maps under the same lock so concurrent misses share the
// dispatch even if the first response is still running its result processors.
func (c *queryCache) acquire(
	ctx context.Context, key cacheKey, disp dispatcher.Dispatcher, query *qdata.Query,
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

	go c.dispatch(dispatchCtx, key, flight, disp, query)

	return nil, flight
}

func (c *queryCache) dispatch(
	ctx context.Context, key cacheKey, flight *cacheFlight, disp dispatcher.Dispatcher, query *qdata.Query,
) {
	defer flight.cancel()

	flight.result, flight.err = disp.Dispatch(ctx, query)
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

func (c *queryCache) encode(key cacheKey, result *qdata.Result) *cacheEntry {
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

func (c *queryCache) release(key cacheKey, flight *cacheFlight) {
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
func (c *queryCache) publish(ctx context.Context, key cacheKey, flight *cacheFlight) {
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

func (c *queryCache) remove(element *list.Element) {
	entry, _ := element.Value.(*cacheEntry)
	c.bytes -= entry.size()

	delete(c.entries, entry.key)
	c.lru.Remove(element)
}
