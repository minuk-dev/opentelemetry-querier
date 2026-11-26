package querier

import (
	"context"
	"crypto/sha256"
	_ "embed"
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

//go:embed shared_quota.lua
var admitSharedQuota string

var quotaNamespace = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type sharedQuota struct {
	cfg    SharedQuotaConfig
	client *redis.Client
}

const sharedQuotaTimeout = 250 * time.Millisecond

var (
	errSharedQuotaConfig  = errors.New("shared quota requires a valid namespace and timeout up to 5s")
	errSharedQuotaBucket  = errors.New("shared quota rate must be in 0.001–1000000 with a burst in 1–1000000")
	errSharedQuotaURLFile = errors.New("cannot read shared quota Redis URL file")
	errSharedQuotaURL     = errors.New("invalid shared quota Redis URL")
)

func (bucket QuotaBucket) valid() bool {
	return !math.IsNaN(bucket.Rate) && bucket.Rate >= 0.001 && bucket.Rate <= 1_000_000 &&
		bucket.Burst >= 1 && bucket.Burst <= 1_000_000
}

func newSharedQuota(cfg SharedQuotaConfig) (*sharedQuota, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = sharedQuotaTimeout
	}

	if cfg.Timeout < 0 || cfg.Timeout > 5*time.Second || !quotaNamespace.MatchString(cfg.Namespace) {
		return nil, errSharedQuotaConfig
	}

	for _, bucket := range []QuotaBucket{cfg.Tenant, cfg.Deployment} {
		if !bucket.valid() {
			return nil, errSharedQuotaBucket
		}
	}

	url, err := os.ReadFile(cfg.RedisURLFile)
	if err != nil {
		return nil, errSharedQuotaURLFile
	}

	options, err := redis.ParseURL(strings.TrimSpace(string(url)))
	if err != nil {
		return nil, errSharedQuotaURL
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

	keys := []string{prefix + "tenant:" + hex.EncodeToString(tenant[:]), prefix + "deployment"}

	answer, err := q.client.Eval(ctx, admitSharedQuota, keys,
		q.cfg.Tenant.Rate, q.cfg.Tenant.Burst, q.cfg.Deployment.Rate, q.cfg.Deployment.Burst).Int()
	if err != nil {
		return qerror.New(qerror.CodeUnavailable, "shared quota store unavailable")
	}

	if answer != 1 {
		return qerror.New(qerror.CodeResourceExhausted, "shared query quota exceeded")
	}

	return nil
}
