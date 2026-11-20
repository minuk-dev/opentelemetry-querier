package pipeline_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

type cacheDispatcher struct {
	dispatcher.Base

	handle pipeline.HandlerFunc
}

func (cacheDispatcher) SupportsQueryCache() bool { return true }

func (d cacheDispatcher) Dispatch(ctx context.Context, query *qdata.Query) (*qdata.Result, error) {
	result, err := d.handle(ctx, query)
	if err != nil {
		return nil, err
	}

	return result, nil
}

type cachePolicy struct {
	processor.Base

	deny  bool
	fail  bool
	scope string
	mask  string
}

func (p *cachePolicy) ProcessQuery(_ context.Context, query *qdata.Query) error {
	if p.deny {
		return fmt.Errorf("check request: %w", qerror.New(qerror.CodePermissionDenied, "denied by test policy"))
	}

	query.Plan = qdata.Plan(qdata.SelectNode(qdata.SignalMetrics,
		qdata.LeafPredicate(&qdata.LabelMatcher{Name: "scope", Value: p.scope, Op: qdata.MatchEqual})))

	return nil
}

func (p *cachePolicy) ProcessResult(_ context.Context, _ *qdata.Query, result *qdata.Result) error {
	if p.fail {
		return fmt.Errorf("process response: %w", qerror.New(qerror.CodeInternal, "response processor failed"))
	}

	qdata.Warn(result, "mask", p.mask, "test")

	return nil
}

func cacheQuery() *qdata.Query {
	window := new(qdata.TimeRange)
	window.Start = timestamppb.New(time.Unix(10, 0))
	window.End = timestamppb.New(time.Unix(20, 0))

	query := new(qdata.Query)
	query.Signal = qdata.SignalMetrics
	query.Context = qdata.ContextRange
	query.Range = window
	query.Step = durationpb.New(time.Second)
	query.Plan = qdata.Plan(qdata.SelectNode(qdata.SignalMetrics, nil))

	qdata.SetTenantID(query, "tenant-a")

	return query
}

func TestCacheRunsPolicyAndResponseProcessorsOnHits(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	policy := &cachePolicy{Base: processor.Base{}, deny: false, fail: false, scope: "full", mask: "original"}
	dispatch := func(_ context.Context, query *qdata.Query) (*qdata.Result, error) {
		calls.Add(1)

		result := new(qdata.Result)
		qdata.Warn(result, "backend", query.GetPlan().GetRoot().GetSelect().GetFilter().GetLeaf().GetValue(), "upstream")

		return result, nil
	}
	disp := cacheDispatcher{Base: dispatcher.Base{}, handle: dispatch}
	pipe := pipeline.New("metrics", []processor.Processor{policy}, disp)
	require.NoError(t, pipe.EnableCache(pipeline.CacheConfig{TTL: time.Minute, MaxBytes: 4096}))

	first, err := pipe.Handle(t.Context(), cacheQuery())
	require.NoError(t, err)
	assert.Equal(t, qdata.SignalMetrics, first.GetSignal())
	require.Len(t, first.GetFeedback().GetNotifications(), 2)
	assert.Equal(t, "full", first.GetFeedback().GetNotifications()[0].GetMessage())
	assert.Equal(t, "original", first.GetFeedback().GetNotifications()[1].GetMessage())

	hit, err := pipe.Handle(t.Context(), cacheQuery())
	require.NoError(t, err)
	assert.True(t, proto.Equal(first, hit))
	assert.EqualValues(t, 1, calls.Load())

	first.GetFeedback().GetNotifications()[0].Message = "caller mutation"
	policy.mask = "stricter"
	hit, err = pipe.Handle(t.Context(), cacheQuery())
	require.NoError(t, err)
	require.Len(t, hit.GetFeedback().GetNotifications(), 2)
	assert.Equal(t, "full", hit.GetFeedback().GetNotifications()[0].GetMessage())
	assert.Equal(t, "stricter", hit.GetFeedback().GetNotifications()[1].GetMessage())
	assert.EqualValues(t, 1, calls.Load())

	policy.deny = true
	_, err = pipe.Handle(t.Context(), cacheQuery())
	require.Error(t, err)
	assert.Equal(t, qerror.CodePermissionDenied, qerror.CodeOf(err))
	assert.EqualValues(t, 1, calls.Load())

	policy.deny = false
	policy.scope = "restricted"
	scoped, err := pipe.Handle(t.Context(), cacheQuery())
	require.NoError(t, err)
	assert.Equal(t, "restricted", scoped.GetFeedback().GetNotifications()[0].GetMessage())
	assert.EqualValues(t, 2, calls.Load())
}

func TestCacheKeyDistinguishesEffectiveQueries(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		change func(*qdata.Query)
		want   int32
	}{
		{name: "equivalent plan and transport-only headers", change: func(query *qdata.Query) {
			query.Header = map[string]*qdata.HeaderValues{"User-Agent": {Values: []string{"different"}}}
		}, want: 1},
		{name: "tenant", change: func(query *qdata.Query) { qdata.SetTenantID(query, "other") }, want: 2},
		{name: "authorization metadata", change: func(query *qdata.Query) {
			qdata.SetMetadata(query, "authz.subject", "another-principal")
		}, want: 2},
		{name: "end", change: func(query *qdata.Query) {
			query.Range.End = timestamppb.New(time.Unix(30, 0))
		}, want: 2},
		{name: "step", change: func(query *qdata.Query) { query.Step = durationpb.New(2 * time.Second) }, want: 2},
		{name: "log options", change: func(query *qdata.Query) {
			query.LogOptions = &qdata.LogOptions{Limit: 5, Direction: ""}
		}, want: 2},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32

			disp := cacheDispatcher{Base: dispatcher.Base{}, handle: func(context.Context, *qdata.Query) (*qdata.Result, error) {
				calls.Add(1)

				return new(qdata.Result), nil
			}}
			pipe := pipeline.New("one", nil, disp)
			require.NoError(t, pipe.EnableCache(pipeline.CacheConfig{TTL: time.Minute, MaxBytes: 4096}))

			_, err := pipe.Handle(t.Context(), cacheQuery())
			require.NoError(t, err)

			query := cacheQuery()
			testCase.change(query)
			_, err = pipe.Handle(t.Context(), query)
			require.NoError(t, err)
			assert.Equal(t, testCase.want, calls.Load())

			other := pipeline.New("two", nil, disp)
			require.NoError(t, other.EnableCache(pipeline.CacheConfig{TTL: time.Minute, MaxBytes: 4096}))
			_, err = other.Handle(t.Context(), cacheQuery())
			require.NoError(t, err)
			assert.Equal(t, testCase.want+1, calls.Load(), "different pipeline has its own cache")
		})
	}
}

func TestCacheExpiryEvictionAndUnpinnedQueries(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32

		disp := cacheDispatcher{Base: dispatcher.Base{}, handle: func(context.Context, *qdata.Query) (*qdata.Result, error) {
			calls.Add(1)

			return new(qdata.Result), nil
		}}
		pipe := pipeline.New("metrics", nil, disp)
		require.NoError(t, pipe.EnableCache(pipeline.CacheConfig{TTL: time.Minute, MaxBytes: sha256.Size}))

		handle := func(query *qdata.Query) {
			t.Helper()

			_, err := pipe.Handle(t.Context(), query)
			require.NoError(t, err)
		}

		handle(cacheQuery())
		handle(cacheQuery())
		assert.EqualValues(t, 1, calls.Load())

		time.Sleep(time.Minute)
		handle(cacheQuery())
		assert.EqualValues(t, 2, calls.Load(), "expired entry is dispatched again")

		other := cacheQuery()
		qdata.SetTenantID(other, "tenant-b")
		handle(other)
		handle(cacheQuery())
		assert.EqualValues(t, 4, calls.Load(), "only one key fits in this cache")

		for range 2 {
			implicit := cacheQuery()
			implicit.Context = qdata.ContextInstant
			implicit.Range = nil
			handle(implicit)

			nativeDefault := cacheQuery()
			nativeDefault.Context = qdata.ContextInstant
			qdata.SetMetadata(nativeDefault, qdata.MetadataImplicitEvaluationTime, "true")
			handle(nativeDefault)

			unresolved := cacheQuery()
			unresolved.Metadata = nil
			handle(unresolved)
		}

		assert.EqualValues(t, 10, calls.Load(), "implicit time and unresolved tenants bypass caching")
	})
}

func TestCacheDoesNotStoreFailedResponses(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	policy := &cachePolicy{Base: processor.Base{}, deny: false, fail: true, scope: "full", mask: ""}
	disp := cacheDispatcher{Base: dispatcher.Base{}, handle: func(context.Context, *qdata.Query) (*qdata.Result, error) {
		calls.Add(1)

		return new(qdata.Result), nil
	}}
	pipe := pipeline.New("metrics", []processor.Processor{policy}, disp)
	require.NoError(t, pipe.EnableCache(pipeline.CacheConfig{TTL: time.Minute, MaxBytes: 4096}))

	_, err := pipe.Handle(t.Context(), cacheQuery())
	require.Error(t, err)

	policy.fail = false
	_, err = pipe.Handle(t.Context(), cacheQuery())
	require.NoError(t, err)
	assert.EqualValues(t, 2, calls.Load())
}

func TestCacheSkipsOversizedAndPartialResults(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		response string
		capacity int
	}{
		{name: "oversized", response: `{"signal":"SIGNAL_LOGS"}`, capacity: sha256.Size},
		{name: "upstream timeout", response: `{"logs":{"searchMetadata":{"timedOut":true}}}`, capacity: 4096},
		{name: "upstream shard failure", response: `{"logs":{"searchMetadata":{"shards":{"failed":"1"}}}}`, capacity: 4096},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32

			raw := new(qdata.Result)
			require.NoError(t, protojson.Unmarshal([]byte(testCase.response), raw))

			disp := cacheDispatcher{Base: dispatcher.Base{}, handle: func(context.Context, *qdata.Query) (*qdata.Result, error) {
				calls.Add(1)

				return raw, nil
			}}
			pipe := pipeline.New("metrics", nil, disp)
			require.NoError(t, pipe.EnableCache(pipeline.CacheConfig{TTL: time.Minute, MaxBytes: testCase.capacity}))

			for range 2 {
				_, err := pipe.Handle(t.Context(), cacheQuery())
				require.NoError(t, err)
			}

			assert.EqualValues(t, 2, calls.Load())
		})
	}
}

func startCachedRequest(ctx context.Context, pipe *pipeline.Pipeline, query *qdata.Query) <-chan error {
	finished := make(chan error, 1)

	go func() {
		_, err := pipe.Handle(ctx, query)
		finished <- err
	}()

	return finished
}

func TestCacheCoalescesWithoutInheritingFirstDeadline(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32

		unblock := make(chan struct{})
		dispatch := func(ctx context.Context, _ *qdata.Query) (*qdata.Result, error) {
			calls.Add(1)

			select {
			case <-unblock:
				return new(qdata.Result), nil
			case <-ctx.Done():
				return nil, qerror.UpstreamTransportError(ctx.Err())
			}
		}
		disp := cacheDispatcher{Base: dispatcher.Base{}, handle: dispatch}
		pipe := pipeline.New("metrics", nil, disp)
		require.NoError(t, pipe.EnableCache(pipeline.CacheConfig{TTL: time.Minute, MaxBytes: 4096}))

		firstCtx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		first := startCachedRequest(firstCtx, pipe, cacheQuery())

		synctest.Wait()

		second := startCachedRequest(t.Context(), pipe, cacheQuery())
		third := startCachedRequest(t.Context(), pipe, cacheQuery())
		otherTenant := cacheQuery()
		qdata.SetTenantID(otherTenant, "tenant-b")
		other := startCachedRequest(t.Context(), pipe, otherTenant)

		synctest.Wait()

		assert.EqualValues(t, 2, calls.Load(), "three matching queries share one dispatch; the other tenant gets its own")

		time.Sleep(time.Second)

		err := <-first
		require.Error(t, err)
		assert.Equal(t, qerror.CodeDeadlineExceeded, qerror.CodeOf(err))

		close(unblock)
		require.NoError(t, <-second)
		require.NoError(t, <-third)
		require.NoError(t, <-other)
		assert.EqualValues(t, 2, calls.Load())
	})
}

func TestCacheCancelsDispatchWhenLastWaiterLeaves(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32

		stopped := make(chan struct{})
		dispatch := func(ctx context.Context, _ *qdata.Query) (*qdata.Result, error) {
			if calls.Add(1) == 1 {
				<-ctx.Done()
				close(stopped)
			}

			return new(qdata.Result), nil
		}
		disp := cacheDispatcher{Base: dispatcher.Base{}, handle: dispatch}
		pipe := pipeline.New("metrics", nil, disp)
		require.NoError(t, pipe.EnableCache(pipeline.CacheConfig{TTL: time.Minute, MaxBytes: 4096}))

		ctx, cancel := context.WithCancel(t.Context())
		first := startCachedRequest(ctx, pipe, cacheQuery())

		synctest.Wait()

		cancel()

		err := <-first
		require.Error(t, err)
		assert.Equal(t, qerror.CodeCanceled, qerror.CodeOf(err))
		<-stopped

		_, err = pipe.Handle(t.Context(), cacheQuery())
		require.NoError(t, err)
		assert.EqualValues(t, 2, calls.Load(), "canceled operation cannot populate the cache")
	})
}

func TestCacheClearsFailedSharedDispatches(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32

		unblock := make(chan struct{})
		disp := cacheDispatcher{Base: dispatcher.Base{}, handle: func(context.Context, *qdata.Query) (*qdata.Result, error) {
			if calls.Add(1) == 1 {
				<-unblock

				return nil, qerror.New(qerror.CodeDeadlineExceeded, "backend timeout")
			}

			return new(qdata.Result), nil
		}}
		pipe := pipeline.New("metrics", nil, disp)
		require.NoError(t, pipe.EnableCache(pipeline.CacheConfig{TTL: time.Minute, MaxBytes: 4096}))

		first := startCachedRequest(t.Context(), pipe, cacheQuery())
		second := startCachedRequest(t.Context(), pipe, cacheQuery())

		synctest.Wait()

		assert.EqualValues(t, 1, calls.Load())
		close(unblock)

		for _, pending := range []<-chan error{first, second} {
			err := <-pending
			require.Error(t, err)
			assert.Equal(t, qerror.CodeDeadlineExceeded, qerror.CodeOf(err))
		}

		_, err := pipe.Handle(t.Context(), cacheQuery())
		require.NoError(t, err)
		assert.EqualValues(t, 2, calls.Load())
	})
}
