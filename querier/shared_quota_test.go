//go:build integration

package querier

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/stretchr/testify/require"
)

func TestSharedQuotaAcrossReplicas(t *testing.T) {
	endpoint := os.Getenv("QUERIER_TEST_REDIS_URL")
	if endpoint == "" {
		t.Skip("set QUERIER_TEST_REDIS_URL to an isolated Redis")
	}
	file := filepath.Join(t.TempDir(), "redis-url")
	require.NoError(t, os.WriteFile(file, []byte(endpoint), 0600))
	cfg := SharedQuotaConfig{RedisURLFile: file, Namespace: rand.Text(), Timeout: time.Second,
		Tenant: QuotaBucket{Rate: 0.001, Burst: 3}, Deployment: QuotaBucket{Rate: 0.001, Burst: 4}}
	first, err := newSharedQuota(cfg)
	require.NoError(t, err)
	second, err := newSharedQuota(cfg)
	require.NoError(t, err)
	defer second.client.Close()
	ctx := qdata.WithIdentity(t.Context(), qdata.Identity{Authority: "test", Principal: "alice", Tenant: "a"})
	var admitted atomic.Int64
	var requests sync.WaitGroup
	for i := range 20 {
		replica := first
		if i%2 == 0 {
			replica = second
		}
		requests.Go(func() {
			err := replica.admit(ctx)
			if err == nil {
				admitted.Add(1)
			} else if qerror.CodeOf(err) != qerror.CodeResourceExhausted {
				t.Errorf("admission: %v", err)
			}
		})
	}
	requests.Wait()
	require.EqualValues(t, 3, admitted.Load())
	require.NoError(t, first.client.Close())
	restarted, err := newSharedQuota(cfg)
	require.NoError(t, err)
	defer restarted.client.Close()
	require.Equal(t, qerror.CodeResourceExhausted, qerror.CodeOf(restarted.admit(ctx)))
	other := qdata.WithIdentity(t.Context(), qdata.Identity{Authority: "test", Principal: "bob", Tenant: "b"})
	require.NoError(t, restarted.admit(other))
	require.Equal(t, qerror.CodeResourceExhausted, qerror.CodeOf(second.admit(other)))
}
