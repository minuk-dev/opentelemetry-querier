package authratelimitprocessor_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/processor/authratelimitprocessor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// tenantQuery builds a Query already carrying a resolved tenant id, the shape the
// tenant processor hands to the limiter.
func tenantQuery(tenant string) *qdata.Query {
	return &qdata.Query{Metadata: map[string]string{qdata.MetadataTenantID: tenant}}
}

// perTenant builds a limiter-only processor (no bearer check) keyed by tenant,
// with a refill rate slow enough that no bucket meaningfully refills during the
// test — every assertion is then about bucket bookkeeping, not wall-clock timing.
func perTenant(t *testing.T, maxKeys, burst int) *authratelimitprocessor.Processor {
	t.Helper()

	return authratelimitprocessor.New(authratelimitprocessor.Config{
		RequireBearer:     false,
		Tokens:            nil,
		RequestsPerSecond: 0.001,
		Burst:             burst,
		PerTenant:         true,
		MaxKeys:           maxKeys,
	})
}

// allowed reports whether the query passed the limiter, failing the test on any
// error other than the expected ResourceExhausted.
func allowed(t *testing.T, proc *authratelimitprocessor.Processor, query *qdata.Query) bool {
	t.Helper()

	err := proc.ProcessQuery(context.Background(), query)
	if err == nil {
		return true
	}

	require.Equal(t, qerror.CodeResourceExhausted, qerror.CodeOf(err))

	return false
}

// TestKeyChurnIsBoundedAndLimited is issue #68: the per-tenant bucket map was
// only ever written to, and every new key started at full burst. A caller varying
// the tenant id therefore got a fresh full allowance on every request — never
// limited — while growing the map for the process lifetime.
//
// With the key space capped, the first MaxKeys ids get their own bucket and
// everyone after them shares the overflow bucket. Counting the allowed requests
// proves both halves at once: the count is bounded, so the churn is limited, and
// it is bounded *because* the map stopped allocating buckets at the cap.
func TestKeyChurnIsBoundedAndLimited(t *testing.T) {
	t.Parallel()

	const (
		maxKeys  = 8
		requests = 200
	)

	proc := perTenant(t, maxKeys, 1)

	allowedCount := 0

	for i := range requests {
		if allowed(t, proc, tenantQuery("tenant-"+strconv.Itoa(i))) {
			allowedCount++
		}
	}

	// maxKeys buckets of one token each, plus the single token in the shared
	// admission bucket that every remaining id competes for.
	assert.Equal(t, maxKeys+1, allowedCount,
		"a caller cycling tenant ids must not mint a fresh full bucket per request")
}

// TestPerTenantKeepsIsolationUnderTheCap guards the fix against over-correcting:
// distinct tenants below the cap still get independent buckets, and a returning
// tenant does not get its bucket reset.
func TestPerTenantKeepsIsolationUnderTheCap(t *testing.T) {
	t.Parallel()

	proc := perTenant(t, 8, 1)

	assert.True(t, allowed(t, proc, tenantQuery("acme")), "acme's first request is under its own limit")
	assert.True(t, allowed(t, proc, tenantQuery("globex")), "globex has its own bucket, undisturbed by acme")
	assert.False(t, allowed(t, proc, tenantQuery("acme")), "acme's bucket is drained, not re-created")
}

// TestBurstDefaultsToCeil pins the documented default: Burst is ceil(rps), so a
// fractional rate does not silently truncate to a smaller bucket (rps 1.9 gave a
// capacity of 1, not 2).
func TestBurstDefaultsToCeil(t *testing.T) {
	t.Parallel()

	proc := authratelimitprocessor.New(authratelimitprocessor.Config{
		RequireBearer:     false,
		Tokens:            nil,
		RequestsPerSecond: 1.9,
		Burst:             0,
		PerTenant:         false,
		MaxKeys:           0,
	})

	assert.True(t, allowed(t, proc, tenantQuery("acme")), "first of ceil(1.9)==2 tokens")
	assert.True(t, allowed(t, proc, tenantQuery("acme")), "second of ceil(1.9)==2 tokens")
	assert.False(t, allowed(t, proc, tenantQuery("acme")), "the bucket holds no more than 2 tokens")
}

// TestResidentTenantSurvivesKeyChurn is the other half of the bound: capping the
// key space is only worth doing if it does not hand the churn a lever against
// the tenants it is meant to protect. A tenant holding a bucket keeps it — the
// evicted entry is always the least recently used one, and the churn can only
// evict as fast as it wins admission tokens.
func TestResidentTenantSurvivesKeyChurn(t *testing.T) {
	t.Parallel()

	const (
		maxKeys = 4
		burst   = 3
	)

	proc := perTenant(t, maxKeys, burst)

	require.True(t, allowed(t, proc, tenantQuery("acme")), "acme takes its first token")

	// Fill the remaining slots, then touch acme so it is the most recently used
	// entry rather than the eviction candidate.
	for _, filler := range []string{"x", "y", "z"} {
		require.True(t, allowed(t, proc, tenantQuery(filler)))
	}

	require.True(t, allowed(t, proc, tenantQuery("acme")), "acme takes its second token")

	// The map is full, so each unseen id costs an admission token. There are
	// burst of them, and each one evicts the least recently used filler.
	for i := range burst {
		assert.True(t, allowed(t, proc, tenantQuery("churn-"+strconv.Itoa(i))),
			"an unseen id is admitted while admission tokens remain")
	}

	assert.False(t, allowed(t, proc, tenantQuery("churn-out")),
		"admission is drained, so the churn is capped at the configured rate")

	assert.True(t, allowed(t, proc, tenantQuery("acme")),
		"acme was never the least recently used entry, so it kept its own bucket")
	assert.False(t, allowed(t, proc, tenantQuery("acme")),
		"and that bucket is its own: three tokens, not a fresh one")
}

// TestOverLongTenantIDsAreBoundedButDistinct covers the byte side of the bound:
// max_keys caps how many entries exist, not how many bytes each retains, and
// nothing upstream limits a tenant id's length. Over-long ids are keyed by
// digest, which must not merge two tenants into one bucket.
func TestOverLongTenantIDsAreBoundedButDistinct(t *testing.T) {
	t.Parallel()

	proc := perTenant(t, 8, 1)

	first := strings.Repeat("a", 4096)
	second := first[:len(first)-1] + "b"

	assert.True(t, allowed(t, proc, tenantQuery(first)), "the first over-long id gets a bucket")
	assert.False(t, allowed(t, proc, tenantQuery(first)), "and is limited by it")
	assert.True(t, allowed(t, proc, tenantQuery(second)),
		"an id differing in one byte must not collide into the first one's bucket")
}
