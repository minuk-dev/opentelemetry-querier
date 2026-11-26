package querier

import (
	"context"
	"errors"
	"fmt"
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

const (
	athenzRequestTimeout = 5 * time.Second
	defaultAthenzRefresh = 15 * time.Minute
	defaultAthenzMaxAge  = 45 * time.Minute
	maxAthenzPolicyAge   = 24 * time.Hour
	maxAthenzTokenBytes  = 8192
)

var (
	errAthenzEndpoint      = errors.New("athenz zts_url must be an HTTPS ZTS API URL")
	errAthenzIdentifier    = errors.New("invalid Athenz domain, action, resource prefix or tenant")
	errAthenzRefreshBounds = errors.New("athenz policy age must be between the 1s minimum refresh interval and 24h")
	errAthenzStartup       = errors.New("athenz initial policy refresh failed")
)

func (cfg *AthenzIdentityConfig) defaults() {
	if cfg.Action == "" {
		cfg.Action = "query"
	}

	if cfg.ResourcePrefix == "" {
		cfg.ResourcePrefix = "query"
	}

	if cfg.RefreshInterval == 0 {
		cfg.RefreshInterval = defaultAthenzRefresh
	}

	if cfg.MaxPolicyAge == 0 {
		cfg.MaxPolicyAge = defaultAthenzMaxAge
	}
}

func (cfg *AthenzIdentityConfig) validate() error {
	endpoint, err := url.Parse(cfg.ZTSURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" ||
		endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errAthenzEndpoint
	}

	return cfg.validatePolicy()
}

func (cfg *AthenzIdentityConfig) validatePolicy() error {
	for _, id := range []string{cfg.Domain, cfg.Action, cfg.ResourcePrefix, cfg.Tenant} {
		if !athenzIdentifier.MatchString(id) {
			return errAthenzIdentifier
		}
	}

	if cfg.RefreshInterval < time.Second || cfg.MaxPolicyAge < cfg.RefreshInterval ||
		cfg.MaxPolicyAge > maxAthenzPolicyAge {
		return errAthenzRefreshBounds
	}

	return nil
}

func newAthenzIdentity(cfg AthenzIdentityConfig) (*athenzIdentity, error) {
	cfg.defaults()

	err := cfg.validate()
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Timeout:       athenzRequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	keys, err := pubkey.New(pubkey.WithAthenzURL(cfg.ZTSURL), pubkey.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("create Athenz public key provider: %w", err)
	}

	policies, err := policy.New(policy.WithAthenzURL(cfg.ZTSURL), policy.WithAthenzDomains(cfg.Domain),
		policy.WithPubKeyProvider(keys.GetProvider()), policy.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("create Athenz policy provider: %w", err)
	}

	roles, err := role.New(role.WithPubkeyProvider(keys.GetProvider()))
	if err != nil {
		return nil, fmt.Errorf("create Athenz role verifier: %w", err)
	}

	return &athenzIdentity{
		cfg: cfg, verify: roles.ParseAndValidateRoleToken, check: policies.CheckPolicy,
		lastRefresh: atomic.Int64{}, cancel: nil, done: nil,
		refresh: func(ctx context.Context) error {
			err := keys.Update(ctx)
			if err != nil {
				return fmt.Errorf("refresh Athenz keys: %w", err)
			}

			err = policies.Update(ctx)
			if err != nil {
				return fmt.Errorf("refresh Athenz policy: %w", err)
			}

			return nil
		},
	}, nil
}

func (a *athenzIdentity) Start(ctx context.Context, _ component.Host) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("start Athenz refresh: %w", err)
	}

	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	err = a.refresh(ctx)
	if err != nil {
		cancel()

		return errAthenzStartup
	}

	a.lastRefresh.Store(time.Now().UnixNano())
	a.cancel = cancel
	a.done = make(chan struct{})

	go func() {
		defer cancel()
		defer close(a.done)

		ticker := time.NewTicker(a.cfg.RefreshInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				err := a.refresh(ctx)
				if err != nil {
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
		return fmt.Errorf("stop Athenz refresh: %w", ctx.Err())
	}
}

func (a *athenzIdentity) authenticate(ctx context.Context, query *qdata.Query) (pipeline.Principal, error) {
	values := qdata.HeaderValuesFor(query, "Athenz-Role-Auth")
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > maxAthenzTokenBytes {
		return pipeline.Principal{}, qerror.New(qerror.CodeUnauthenticated, "missing or malformed Athenz role token")
	}

	last := time.Unix(0, a.lastRefresh.Load())
	if time.Since(last) > a.cfg.MaxPolicyAge {
		return pipeline.Principal{}, qerror.New(qerror.CodeUnavailable, "Athenz policy unavailable")
	}

	token, err := a.verify(values[0])
	if err != nil || !validAthenzRole(token) {
		return pipeline.Principal{}, qerror.New(qerror.CodeUnauthenticated, "invalid Athenz role token")
	}

	if token.Domain != a.cfg.Domain {
		return pipeline.Principal{}, qerror.New(qerror.CodePermissionDenied, "Athenz token domain rejected")
	}

	err = a.checkResources(ctx, query, token.Roles)
	if err != nil {
		return pipeline.Principal{}, err
	}

	if a.cfg.EnforceLabel != "" {
		query.EnforcedMatchers = append(query.EnforcedMatchers, &qdata.LabelMatcher{
			Name: a.cfg.EnforceLabel, Op: qdata.MatchEqual, Value: a.cfg.Tenant,
		})
	}

	return pipeline.Principal{
		Identity: qdata.Identity{
			Authority: "athenz:" + a.cfg.Domain, Principal: token.Principal,
			Tenant: "", Groups: nil, Roles: token.Roles, Scopes: nil,
		},
		Tenants: []string{a.cfg.Tenant},
	}, nil
}

func (a *athenzIdentity) checkResources(ctx context.Context, query *qdata.Query, roles []string) error {
	signals := qdata.QuerySignals(query)
	if len(signals) == 0 {
		return qerror.New(qerror.CodePermissionDenied, "missing Athenz query resource")
	}

	for _, signal := range signals {
		if signal == qdata.SignalUnspecified {
			return qerror.New(qerror.CodePermissionDenied, "missing Athenz query resource")
		}

		kind := strings.ToLower(strings.TrimPrefix(signal.String(), "SIGNAL_"))

		resource := a.cfg.Domain + ":" + a.cfg.ResourcePrefix + "." + kind

		err := a.check(ctx, a.cfg.Domain, roles, a.cfg.Action, resource)
		if err != nil {
			if ctx.Err() != nil {
				return qerror.UpstreamTransportError(ctx.Err())
			}

			return qerror.New(qerror.CodePermissionDenied, "Athenz policy denied query")
		}
	}

	return nil
}

func validAthenzRole(token *role.Token) bool {
	return token != nil && token.Principal != "" && len(token.Roles) > 0
}
