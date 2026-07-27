package pipeline_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/processor/authratelimitprocessor"
)

// TestAuthRateLimit adds the gateway processor (bearer auth + rate limiting) to
// an otherwise empty chain and asserts its short-circuits end to end: a missing
// or invalid token is rejected with 401 before the dispatcher runs, a valid
// token passes through to the upstream, and an over-limit request gets 429.
func TestAuthRateLimit(t *testing.T) {
	t.Parallel()

	t.Run("missing bearer -> 401", func(t *testing.T) {
		t.Parallel()

		upstream := newUpstream(t)
		base := front(t, upstream, authProc(1000))

		code, body := getWith(t, base, nil)

		assert.Equal(t, http.StatusUnauthorized, code, "body: %s", body)
		assert.Empty(t, upstream.gotQuery(), "an unauthenticated query never reaches the upstream")
	})

	t.Run("invalid bearer -> 401", func(t *testing.T) {
		t.Parallel()

		upstream := newUpstream(t)
		base := front(t, upstream, authProc(1000))

		code, body := getWith(t, base, map[string]string{"Authorization": "Bearer wrong"})

		assert.Equal(t, http.StatusUnauthorized, code, "body: %s", body)
		assert.Empty(t, upstream.gotQuery())
	})

	t.Run("valid bearer passes through", func(t *testing.T) {
		t.Parallel()

		upstream := newUpstream(t)
		base := front(t, upstream, authProc(1000))

		code, body := getWith(t, base, map[string]string{"Authorization": "Bearer dev-token"})

		assert.Equal(t, http.StatusOK, code, "body: %s", body)
		assert.Equal(t, `{__name__="up"}`, upstream.gotQuery(), "the authorized query reaches the upstream")
		assert.Contains(t, body, `"status":"success"`)
	})

	t.Run("over-limit -> 429", func(t *testing.T) {
		t.Parallel()

		upstream := newUpstream(t)
		// rps=1, burst=1: the first request drains the bucket; a second one
		// arriving before it refills a whole token is rate-limited.
		base := front(t, upstream, authProc(1))

		headers := map[string]string{"Authorization": "Bearer dev-token"}

		code, body := getWith(t, base, headers)
		assert.Equal(t, http.StatusOK, code, "first request is under the limit; body: %s", body)

		code, body = getWith(t, base, headers)
		assert.Equal(t, http.StatusTooManyRequests, code, "second request exceeds the limit; body: %s", body)
	})
}

// authProc builds the gateway processor requiring the "dev-token" bearer and
// limiting to rps requests per second with a burst of 1.
func authProc(rps float64) processor.Processor {
	return authratelimitprocessor.New(authratelimitprocessor.Config{
		RequireBearer:     true,
		Tokens:            []string{"dev-token"},
		RequestsPerSecond: rps,
		Burst:             1,
		PerTenant:         false,
	})
}
