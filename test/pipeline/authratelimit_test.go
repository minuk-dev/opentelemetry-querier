package pipeline_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/acceptor"
	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher/prometheusdispatcher"
	"github.com/minuk-dev/opentelemetry-querier/extension/bearertokenauthextension"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/processor/authratelimitprocessor"
	"github.com/minuk-dev/opentelemetry-querier/processor/tenantprocessor"
)

// TestAuthRateLimit adds bearer authentication and a rate limiter to
// an otherwise empty chain and asserts its short-circuits end to end: a missing
// or invalid token is rejected with 401 before the dispatcher runs, a valid
// token passes through to the upstream, and an over-limit request gets 429.
func TestAuthRateLimit(t *testing.T) {
	t.Parallel()

	t.Run("missing bearer -> 401", func(t *testing.T) {
		t.Parallel()

		upstream := newUpstream(t)
		base := authFront(t, upstream, "alice", []string{"acme"}, authProc(1000))

		code, body := getWith(t, base, nil)

		assert.Equal(t, http.StatusUnauthorized, code, "body: %s", body)
		assert.Empty(t, upstream.gotQuery(), "an unauthenticated query never reaches the upstream")
	})

	t.Run("invalid bearer -> 401", func(t *testing.T) {
		t.Parallel()

		upstream := newUpstream(t)
		base := authFront(t, upstream, "alice", []string{"acme"}, authProc(1000))

		code, body := getWith(t, base, map[string]string{"Authorization": "Bearer wrong"})

		assert.Equal(t, http.StatusUnauthorized, code, "body: %s", body)
		assert.Empty(t, upstream.gotQuery())
	})

	t.Run("valid bearer passes through", func(t *testing.T) {
		t.Parallel()

		upstream := newUpstream(t)
		base := authFront(t, upstream, "alice", []string{"acme"}, authProc(1000))

		code, body := getWith(t, base, map[string]string{"Authorization": "Bearer dev-token"})

		assert.Equal(t, http.StatusOK, code, "body: %s", body)
		assert.Equal(t, `{__name__="up"}`, upstream.gotQuery(), "the authorized query reaches the upstream")
		assert.Contains(t, body, `"status":"success"`)
	})

	t.Run("over-limit -> 429", func(t *testing.T) {
		t.Parallel()

		upstream := newUpstream(t)
		// burst=1 with a deliberately tiny refill rate: the first request drains
		// the single token, and refilling one more takes ~1000s, so the second
		// request is rate-limited regardless of any GC/CI/scheduler stall between
		// the two calls. This keeps the assertion independent of wall-clock timing.
		base := authFront(t, upstream, "alice", []string{"acme"}, authProc(0.001))

		headers := map[string]string{"Authorization": "Bearer dev-token"}

		code, body := getWith(t, base, headers)
		assert.Equal(t, http.StatusOK, code, "first request is under the limit; body: %s", body)

		code, body = getWith(t, base, headers)
		assert.Equal(t, http.StatusTooManyRequests, code, "second request exceeds the limit; body: %s", body)
	})
}

// TestAuthRateLimitTenantChurn is issue #68 end to end: with per_tenant keying, a
// caller that varies the tenant id used to get a brand-new full bucket on every
// request — never limited, and every id retained for the process lifetime. The
// key space is now capped, so the churn runs out of buckets and lands on the
// shared overflow one.
func TestAuthRateLimitTenantChurn(t *testing.T) {
	t.Parallel()

	const (
		maxKeys  = 4
		requests = 50
	)

	upstream := newUpstream(t)
	// The limiter reads the tenant id the tenant processor resolves, so it
	// only keys per tenant when it runs after that processor.
	base := front(t, upstream,
		tenantprocessor.New(tenantprocessor.Config{
			Header: tenantHeader, Default: "", Required: false, EnforceLabel: "",
		}),
		perTenantProc(maxKeys),
	)

	okCount := 0

	for i := range requests {
		code, body := getWith(t, base, map[string]string{tenantHeader: "tenant-" + strconv.Itoa(i)})
		if code == http.StatusOK {
			okCount++

			continue
		}

		assert.Equal(t, http.StatusTooManyRequests, code, "body: %s", body)
	}

	// maxKeys buckets of one token each, plus the single token in the shared
	// overflow bucket the remaining ids drain together.
	assert.Equal(t, maxKeys+1, okCount, "a fresh tenant id must not buy a fresh full burst")
}

// authProc limits to rps requests per second with a burst of 1. A fractional rps starves
// the refill so over-limit tests stay independent of wall-clock timing.
func authProc(rps float64) processor.Processor {
	return authratelimitprocessor.New(authratelimitprocessor.Config{

		RequestsPerSecond: rps,
		Burst:             1,
		PerTenant:         false,
		MaxKeys:           0,
	})
}

// perTenantProc builds an unauthenticated, per-tenant limiter with a burst of 1
// and a starved refill rate, so every allowed request is a bucket that existed
// rather than one the elapsed wall-clock refilled.
func perTenantProc(maxKeys int) processor.Processor {
	return authratelimitprocessor.New(authratelimitprocessor.Config{

		RequestsPerSecond: 0.001,
		Burst:             1,
		PerTenant:         true,
		MaxKeys:           maxKeys,
	})
}

func TestPerTenantRateLimit(t *testing.T) {
	t.Parallel()

	upstream := newUpstream(t)
	tenant := tenantprocessor.New(tenantprocessor.Config{
		Header: tenantHeader, Default: "", Required: false, EnforceLabel: "",
	})
	limiter := authratelimitprocessor.New(authratelimitprocessor.Config{
		RequestsPerSecond: 0.001, Burst: 1, PerTenant: true,
		MaxKeys: 0,
	})
	base := authFront(t, upstream, "alice", []string{"a", "b"}, tenant, limiter)
	headers := map[string]string{"Authorization": "Bearer dev-token"}
	code, body := getWith(t, base, headers)
	assert.Equal(t, http.StatusForbidden, code, "unresolved tenant: %s", body)

	headers[tenantHeader] = "a"
	code, body = getWith(t, base, headers)
	assert.Equal(t, http.StatusOK, code, "first request from A: %s", body)

	headers[tenantHeader] = "b"
	code, body = getWith(t, base, headers)
	assert.Equal(t, http.StatusOK, code, "first request from B: %s", body)

	headers[tenantHeader] = "a"
	code, body = getWith(t, base, headers)
	assert.Equal(t, http.StatusTooManyRequests, code, "second request from A: %s", body)
}

func authFront(
	t *testing.T, upstream *fakeUpstream, principal string, tenants []string, procs ...processor.Processor,
) string {
	t.Helper()

	factory := bearertokenauthextension.NewFactory()
	authenticator, err := factory.CreateExtension(t.Context(), component.Settings{
		ID:     component.NewID(component.MustNewType("bearertokenauth")),
		Logger: nil, BuildInfo: component.BuildInfo{Command: "test", Version: "test"},
	}, &bearertokenauthextension.Config{
		Authority: "test", Principal: principal, Tenants: tenants, Tokens: []string{"dev-token"},
	})
	require.NoError(t, err)

	verifier, ok := authenticator.(acceptor.ServerAuthenticator)
	require.True(t, ok)
	require.NoError(t, verifier.Start(t.Context(), nil))
	t.Cleanup(func() { assert.NoError(t, verifier.Shutdown(context.Background())) })

	disp := prometheusdispatcher.New(
		prometheusdispatcher.Config{Endpoint: upstream.server.URL, TenantHeader: tenantHeader, Timeout: 0},
	)
	pipe := pipeline.New("metrics", procs, disp)

	return frontHandler(t, acceptor.WithAuthentication(pipe, verifier, tenantHeader, 0))
}
