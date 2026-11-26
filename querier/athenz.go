package querier

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/AthenZ/athenz-authorizer/v5/policy"
	"github.com/AthenZ/athenz-authorizer/v5/pubkey"
	"github.com/AthenZ/athenz-authorizer/v5/role"
	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// AthenzIdentityConfig authorizes signed Athenz role tokens for a fixed tenant.
type AthenzIdentityConfig struct {
	ZTSURL          string        `mapstructure:"zts_url"`
	Domain          string        `mapstructure:"domain"`
	Action          string        `mapstructure:"action"`
	ResourcePrefix  string        `mapstructure:"resource_prefix"`
	Tenant          string        `mapstructure:"tenant"`
	EnforceLabel    string        `mapstructure:"enforce_label"`
	RefreshInterval time.Duration `mapstructure:"refresh_interval"`
	MaxPolicyAge    time.Duration `mapstructure:"max_policy_age"`
}

var athenzIdentifier = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,127}$`)

type athenzIdentity struct {
	cfg         AthenzIdentityConfig
	verify      func(string) (*role.Token, error)
	check       func(context.Context, string, []string, string, string) error
	refresh     func(context.Context) error
	lastRefresh atomic.Int64
	cancel      context.CancelFunc
	done        chan struct{}
}

func newAthenzIdentity(cfg AthenzIdentityConfig) (*athenzIdentity, error) {
	endpoint, err := url.Parse(cfg.ZTSURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("Athenz zts_url must be an HTTPS ZTS API URL")
	}
	if cfg.Action == "" {
		cfg.Action = "query"
	}
	if cfg.ResourcePrefix == "" {
		cfg.ResourcePrefix = "query"
	}
	for _, id := range []string{cfg.Domain, cfg.Action, cfg.ResourcePrefix, cfg.Tenant} {
		if !athenzIdentifier.MatchString(id) {
			return nil, errors.New("invalid Athenz domain, action, resource prefix or tenant")
		}
	}
	if cfg.RefreshInterval == 0 {
		cfg.RefreshInterval = 15 * time.Minute
	}
	if cfg.MaxPolicyAge == 0 {
		cfg.MaxPolicyAge = 45 * time.Minute
	}
	if cfg.RefreshInterval < time.Second || cfg.MaxPolicyAge < cfg.RefreshInterval || cfg.MaxPolicyAge > 24*time.Hour {
		return nil, errors.New("Athenz refresh interval must be at least 1s and max policy age must be between the refresh interval and 24h")
	}

	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	keys, err := pubkey.New(pubkey.WithAthenzURL(cfg.ZTSURL), pubkey.WithHTTPClient(client))
	if err != nil {
		return nil, err
	}
	policies, err := policy.New(policy.WithAthenzURL(cfg.ZTSURL), policy.WithAthenzDomains(cfg.Domain),
		policy.WithPubKeyProvider(keys.GetProvider()), policy.WithHTTPClient(client))
	if err != nil {
		return nil, err
	}
	roles, err := role.New(role.WithPubkeyProvider(keys.GetProvider()))
	if err != nil {
		return nil, err
	}

	return &athenzIdentity{
		cfg: cfg, verify: roles.ParseAndValidateRoleToken, check: policies.CheckPolicy,
		refresh: func(ctx context.Context) error {
			if err := keys.Update(ctx); err != nil {
				return err
			}
			return policies.Update(ctx)
		},
	}, nil
}

func (a *athenzIdentity) Start(ctx context.Context, _ component.Host) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	if err := a.refresh(ctx); err != nil {
		cancel()
		return errors.New("Athenz initial policy refresh failed")
	}
	a.lastRefresh.Store(time.Now().UnixNano())
	a.cancel = cancel
	a.done = make(chan struct{})
	go func() {
		defer close(a.done)
		ticker := time.NewTicker(a.cfg.RefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := a.refresh(ctx); err != nil {
					slog.Warn("Athenz policy refresh failed", "domain", a.cfg.Domain)
					continue
				}
				a.lastRefresh.Store(time.Now().UnixNano())
			}
		}
	}()
	return nil
}

func (a *athenzIdentity) Shutdown(ctx context.Context) error {
	if a.cancel == nil {
		return nil
	}
	a.cancel()
	select {
	case <-a.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *athenzIdentity) authenticate(ctx context.Context, q *qdata.Query) (pipeline.Principal, error) {
	values := qdata.HeaderValuesFor(q, "Athenz-Role-Auth")
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 8192 {
		return pipeline.Principal{}, qerror.New(qerror.CodeUnauthenticated, "missing or malformed Athenz role token")
	}
	last := time.Unix(0, a.lastRefresh.Load())
	if time.Since(last) > a.cfg.MaxPolicyAge {
		return pipeline.Principal{}, qerror.New(qerror.CodeUnavailable, "Athenz policy unavailable")
	}
	token, err := a.verify(values[0])
	if err != nil || token == nil || token.Principal == "" || len(token.Roles) == 0 {
		return pipeline.Principal{}, qerror.New(qerror.CodeUnauthenticated, "invalid Athenz role token")
	}
	if token.Domain != a.cfg.Domain {
		return pipeline.Principal{}, qerror.New(qerror.CodePermissionDenied, "Athenz token domain rejected")
	}
	signals := qdata.QuerySignals(q)
	if len(signals) == 0 {
		return pipeline.Principal{}, qerror.New(qerror.CodePermissionDenied, "missing Athenz query resource")
	}
	for _, signal := range signals {
		if signal == qdata.SignalUnspecified {
			return pipeline.Principal{}, qerror.New(qerror.CodePermissionDenied, "missing Athenz query resource")
		}
		kind := strings.ToLower(strings.TrimPrefix(signal.String(), "SIGNAL_"))
		resource := a.cfg.Domain + ":" + a.cfg.ResourcePrefix + "." + kind
		if err := a.check(ctx, a.cfg.Domain, token.Roles, a.cfg.Action, resource); err != nil {
			if ctx.Err() != nil {
				return pipeline.Principal{}, qerror.UpstreamTransportError(ctx.Err())
			}
			return pipeline.Principal{}, qerror.New(qerror.CodePermissionDenied, "Athenz policy denied query")
		}
	}
	if a.cfg.EnforceLabel != "" {
		q.EnforcedMatchers = append(q.EnforcedMatchers, &qdata.LabelMatcher{Name: a.cfg.EnforceLabel, Op: qdata.MatchEqual, Value: a.cfg.Tenant})
	}
	return pipeline.Principal{
		Identity: qdata.Identity{Authority: "athenz:" + a.cfg.Domain, Principal: token.Principal, Roles: token.Roles},
		Tenants:  []string{a.cfg.Tenant},
	}, nil
}
