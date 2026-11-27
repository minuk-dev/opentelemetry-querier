// Package athenzauthextension authenticates signed Athenz role tokens.
package athenzauthextension

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/AthenZ/athenz-authorizer/v5/policy"
	"github.com/AthenZ/athenz-authorizer/v5/pubkey"
	"github.com/AthenZ/athenz-authorizer/v5/role"
	"github.com/minuk-dev/opentelemetry-querier/acceptor"
	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/component/extension"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// Config verifies signed Athenz role tokens for a fixed tenant.
type Config struct {
	ZTSURL          string        `mapstructure:"zts_url"`
	Domain          string        `mapstructure:"domain"`
	Tenant          string        `mapstructure:"tenant"`
	RefreshInterval time.Duration `mapstructure:"refresh_interval"`
	MaxKeyAge       time.Duration `mapstructure:"max_key_age"`
}

var athenzIdentifier = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,127}$`)

// Authenticator verifies role tokens using refreshed Athenz public keys.
type Authenticator struct {
	cfg         Config
	verify      func(string) (*role.Token, error)
	keys        pubkey.Provider
	client      *http.Client
	refresh     func(context.Context) error
	lastRefresh atomic.Int64
	cancel      context.CancelFunc
	done        chan struct{}
}

const (
	athenzRequestTimeout = 5 * time.Second
	defaultAthenzRefresh = 15 * time.Minute
	defaultAthenzMaxAge  = 45 * time.Minute
	maxAthenzKeyAge      = 24 * time.Hour
	maxAthenzTokenBytes  = 8192
)

var (
	errAthenzEndpoint      = errors.New("athenz zts_url must be an HTTPS ZTS API URL")
	errAthenzIdentifier    = errors.New("invalid Athenz domain or tenant")
	errAthenzRefreshBounds = errors.New("athenz key age must be between the 1s minimum refresh interval and 24h")
	errAthenzStartup       = errors.New("athenz initial key refresh failed")
)

func (cfg *Config) defaults() {
	if cfg.RefreshInterval == 0 {
		cfg.RefreshInterval = defaultAthenzRefresh
	}

	if cfg.MaxKeyAge == 0 {
		cfg.MaxKeyAge = defaultAthenzMaxAge
	}
}

func (cfg *Config) validate() error {
	endpoint, err := url.Parse(cfg.ZTSURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" ||
		endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return errAthenzEndpoint
	}

	return cfg.validateKeys()
}

func (cfg *Config) validateKeys() error {
	for _, id := range []string{cfg.Domain, cfg.Tenant} {
		if !athenzIdentifier.MatchString(id) {
			return errAthenzIdentifier
		}
	}

	if cfg.RefreshInterval < time.Second || cfg.MaxKeyAge < cfg.RefreshInterval ||
		cfg.MaxKeyAge > maxAthenzKeyAge {
		return errAthenzRefreshBounds
	}

	return nil
}

func newAuthenticator(cfg Config) (*Authenticator, error) {
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

	roles, err := role.New(role.WithPubkeyProvider(keys.GetProvider()))
	if err != nil {
		return nil, fmt.Errorf("create Athenz role verifier: %w", err)
	}

	return &Authenticator{
		cfg: cfg, verify: roles.ParseAndValidateRoleToken,
		keys: keys.GetProvider(), client: client, refresh: keys.Update,
		lastRefresh: atomic.Int64{}, cancel: nil, done: nil,
	}, nil
}

// PolicyCache refreshes and reads policies signed by Athenz.
type PolicyCache struct {
	Update      func(context.Context) error
	CheckPolicy func(context.Context, string, []string, string, string) error
}

// NewPolicyCache creates the signed policy cache for this authenticator's domain.
func (a *Authenticator) NewPolicyCache() (PolicyCache, error) {
	cache, err := policy.New(policy.WithAthenzURL(a.cfg.ZTSURL), policy.WithAthenzDomains(a.cfg.Domain),
		policy.WithPubKeyProvider(a.keys), policy.WithHTTPClient(a.client))
	if err != nil {
		return PolicyCache{}, fmt.Errorf("create Athenz policy cache: %w", err)
	}

	return PolicyCache{Update: cache.Update, CheckPolicy: cache.CheckPolicy}, nil
}

// AllowsIdentity reports whether the identity belongs to this Athenz domain and tenant.
func (a *Authenticator) AllowsIdentity(identity qdata.Identity) bool {
	return identity.Authority == "athenz:"+a.cfg.Domain && identity.Tenant == a.cfg.Tenant
}

// Domain returns the Athenz domain.
func (a *Authenticator) Domain() string { return a.cfg.Domain }

// Start loads keys and starts refreshing them.
func (a *Authenticator) Start(startupCtx context.Context, _ component.Host) error {
	err := startupCtx.Err()
	if err != nil {
		return fmt.Errorf("start Athenz refresh: %w", err)
	}

	ctx, cancel := context.WithCancel(context.WithoutCancel(startupCtx))

	err = a.refresh(startupCtx)
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
					slog.Warn("Athenz key refresh failed", "domain", a.cfg.Domain)

					continue
				}

				a.lastRefresh.Store(time.Now().UnixNano())
			}
		}
	}()

	return nil
}

// Shutdown stops the background key refresh.
func (a *Authenticator) Shutdown(ctx context.Context) error {
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

// Authenticate verifies a signed role token and resolves the principal.
func (a *Authenticator) Authenticate(_ context.Context, headers http.Header) (acceptor.Principal, error) {
	values := headers.Values("Athenz-Role-Auth")
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > maxAthenzTokenBytes {
		return acceptor.Principal{}, qerror.New(qerror.CodeUnauthenticated, "missing or malformed Athenz role token")
	}

	last := time.Unix(0, a.lastRefresh.Load())
	if time.Since(last) > a.cfg.MaxKeyAge {
		return acceptor.Principal{}, qerror.New(qerror.CodeUnavailable, "Athenz keys unavailable")
	}

	token, err := a.verify(values[0])
	if err != nil || !validAthenzRole(token) {
		return acceptor.Principal{}, qerror.New(qerror.CodeUnauthenticated, "invalid Athenz role token")
	}

	if token.Domain != a.cfg.Domain {
		return acceptor.Principal{}, qerror.New(qerror.CodePermissionDenied, "Athenz token domain rejected")
	}

	return acceptor.Principal{
		Identity: qdata.Identity{
			Authority: "athenz:" + a.cfg.Domain, Principal: token.Principal,
			Tenant: "", Groups: nil, Roles: token.Roles, Scopes: nil,
		},
		Tenants: []string{a.cfg.Tenant},
	}, nil
}

func validAthenzRole(token *role.Token) bool {
	return token != nil && token.Principal != "" && len(token.Roles) > 0
}

var errInvalidConfig = errors.New("athenzauth: invalid config type")

// NewFactory registers the athenzauth extension.
func NewFactory() extension.Factory {
	return extension.NewFactory(
		component.MustNewType("athenzauth"), func() component.Config { return new(Config) }, create,
	)
}

func create(_ context.Context, _ component.Settings, cfg component.Config) (extension.Extension, error) {
	config, ok := cfg.(*Config)
	if !ok {
		return nil, errInvalidConfig
	}

	return newAuthenticator(*config)
}
