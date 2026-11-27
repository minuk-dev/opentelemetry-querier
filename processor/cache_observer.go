package processor

import "context"

type cacheObserverKey struct{}

// WithCacheObserver attaches an observer for cache lookups to a request.
func WithCacheObserver(ctx context.Context, observe func(context.Context, bool)) context.Context {
	return context.WithValue(ctx, cacheObserverKey{}, observe)
}

// ObserveCache records a cache lookup when a request has telemetry enabled.
func ObserveCache(ctx context.Context, hit bool) {
	if observer, ok := ctx.Value(cacheObserverKey{}).(func(context.Context, bool)); ok {
		observer(ctx, hit)
	}
}
