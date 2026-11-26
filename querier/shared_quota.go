package querier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/redis/go-redis/v9"
)

// SharedQuotaConfig enforces a verified tenant quota and a deployment quota together.
type SharedQuotaConfig struct {
	RedisURLFile string        `mapstructure:"redis_url_file"`
	Namespace    string        `mapstructure:"namespace"`
	Timeout      time.Duration `mapstructure:"timeout"`
	Tenant       QuotaBucket   `mapstructure:"tenant"`
	Deployment   QuotaBucket   `mapstructure:"deployment"`
}

// QuotaBucket configures a token bucket in queries per second.
type QuotaBucket struct {
	Rate  float64 `mapstructure:"rate"`
	Burst int     `mapstructure:"burst"`
}

var quotaNamespace = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type sharedQuota struct {
	cfg    SharedQuotaConfig
	client *redis.Client
}

func newSharedQuota(cfg SharedQuotaConfig) (*sharedQuota, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 250 * time.Millisecond
	}
	if cfg.Timeout < 0 || cfg.Timeout > 5*time.Second || !quotaNamespace.MatchString(cfg.Namespace) {
		return nil, errors.New("shared quota requires a valid namespace and timeout up to 5s")
	}
	for _, bucket := range []QuotaBucket{cfg.Tenant, cfg.Deployment} {
		if math.IsNaN(bucket.Rate) || bucket.Rate < 0.001 || bucket.Rate > 1_000_000 || bucket.Burst < 1 || bucket.Burst > 1_000_000 {
			return nil, errors.New("shared quota rate must be in 0.001–1000000 with a burst in 1–1000000")
		}
	}
	url, err := os.ReadFile(cfg.RedisURLFile)
	if err != nil {
		return nil, errors.New("cannot read shared quota Redis URL file")
	}
	options, err := redis.ParseURL(strings.TrimSpace(string(url)))
	if err != nil {
		return nil, errors.New("invalid shared quota Redis URL")
	}
	options.MaxRetries = -1
	options.ContextTimeoutEnabled = true
	options.DialTimeout = cfg.Timeout
	options.ReadTimeout = cfg.Timeout
	options.WriteTimeout = cfg.Timeout
	return &sharedQuota{cfg: cfg, client: redis.NewClient(options)}, nil
}

func (q *sharedQuota) admit(ctx context.Context) error {
	identity, ok := qdata.IdentityFromContext(ctx)
	if !ok || identity.Tenant == "" {
		return qerror.New(qerror.CodeUnauthenticated, "shared quota requires verified tenant")
	}
	ctx, cancel := context.WithTimeout(ctx, q.cfg.Timeout)
	defer cancel()
	tenant := sha256.Sum256([]byte(identity.Tenant))
	prefix := "querier-quota:{" + q.cfg.Namespace + "}:"
	answer, err := q.client.Eval(ctx, admitSharedQuota, []string{prefix + "tenant:" + hex.EncodeToString(tenant[:]), prefix + "deployment"},
		q.cfg.Tenant.Rate, q.cfg.Tenant.Burst, q.cfg.Deployment.Rate, q.cfg.Deployment.Burst).Int()
	if err != nil {
		return qerror.New(qerror.CodeUnavailable, "shared quota store unavailable")
	}
	if answer != 1 {
		return qerror.New(qerror.CodeResourceExhausted, "shared query quota exceeded")
	}
	return nil
}

const admitSharedQuota = `
local stamp = redis.call('TIME')
local now = tonumber(stamp[1]) * 1000 + math.floor(tonumber(stamp[2]) / 1000)
local function available(key, rate, burst)
  local saved = redis.call('HMGET', key, 'tokens', 'at')
  if not saved[1] then return burst, now end
  local previous = tonumber(saved[2])
  local effective = math.max(now, previous)
  return math.min(burst, tonumber(saved[1]) + (effective - previous) * rate / 1000), effective
end
local rates = {tonumber(ARGV[1]), tonumber(ARGV[3])}
local bursts = {tonumber(ARGV[2]), tonumber(ARGV[4])}
local balance, updated = {}, {}
for i = 1, 2 do
  balance[i], updated[i] = available(KEYS[i], rates[i], bursts[i])
  if balance[i] < 1 then return 0 end
end
for i = 1, 2 do
  redis.call('HSET', KEYS[i], 'tokens', string.format('%.17g', balance[i] - 1), 'at', updated[i])
  redis.call('PEXPIRE', KEYS[i], math.ceil(bursts[i] * 1000 / rates[i]))
end
return 1
`
