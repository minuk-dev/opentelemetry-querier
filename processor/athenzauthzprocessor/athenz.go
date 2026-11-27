// Package athenzauthzprocessor checks signed Athenz policy for authenticated queries.
package athenzauthzprocessor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/extension/athenzauthextension"
	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// Config selects the authenticated domain and controls query authorization.
type Config struct {
	Authenticator   string        `mapstructure:"authenticator"`
	Action          string        `mapstructure:"action"`
	ResourcePrefix  string        `mapstructure:"resource_prefix"`
	EnforceLabel    string        `mapstructure:"enforce_label"`
	RefreshInterval time.Duration `mapstructure:"refresh_interval"`
	MaxPolicyAge    time.Duration `mapstructure:"max_policy_age"`
}

const (
	defaultRefresh      = 15 * time.Minute
	defaultMaxPolicyAge = 45 * time.Minute
	maxPolicyAge        = 24 * time.Hour
)

var (
	identifier   = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,127}$`)
	errConfig    = errors.New("athenzauthz: invalid authenticator, action, resource prefix or refresh window")
	errExtension = errors.New("athenzauthz: expected an enabled athenzauth extension")
)

type policyProcessor struct {
	processor.Base

	config      Config
	domain      string
	allows      func(qdata.Identity) bool
	check       func(context.Context, string, []string, string, string) error
	lastRefresh atomic.Int64
	cancel      context.CancelFunc
	done        chan struct{}
}

// NewFactory registers the Athenz authorization processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(component.MustNewType("athenzauthz"), defaultConfig, create)
}

func defaultConfig() component.Config {
	return &Config{
		Authenticator: "", Action: "query", ResourcePrefix: "query", EnforceLabel: "",
		RefreshInterval: defaultRefresh, MaxPolicyAge: defaultMaxPolicyAge,
	}
}

func create(_ context.Context, _ component.Settings, cfg component.Config) (processor.Processor, error) {
	config, ok := cfg.(*Config)
	if !ok || config.Authenticator == "" || !identifier.MatchString(config.Action) ||
		!identifier.MatchString(config.ResourcePrefix) || config.RefreshInterval < time.Second ||
		config.MaxPolicyAge < config.RefreshInterval || config.MaxPolicyAge > maxPolicyAge {
		return nil, errConfig
	}

	return &policyProcessor{
		Base: processor.Base{}, config: *config, domain: "", allows: nil, check: nil,
		lastRefresh: atomic.Int64{}, cancel: nil, done: nil,
	}, nil
}

func (p *policyProcessor) Start(ctx context.Context, host component.Host) error {
	source, ok := host.GetComponent("extension", p.config.Authenticator).(*athenzauthextension.Authenticator)
	if !ok {
		return errExtension
	}

	cache, err := source.NewPolicyCache()
	if err != nil {
		return fmt.Errorf("initialize Athenz policy: %w", err)
	}

	err = cache.Update(ctx)
	if err != nil {
		return fmt.Errorf("refresh initial Athenz policy: %w", err)
	}

	p.domain = source.Domain()
	p.allows = source.AllowsIdentity
	p.check = cache.CheckPolicy
	p.lastRefresh.Store(time.Now().UnixNano())

	background, cancel := context.WithCancel(context.WithoutCancel(ctx))
	p.cancel = cancel

	p.done = make(chan struct{})
	go p.refresh(background, cache.Update)

	return nil
}

func (p *policyProcessor) Shutdown(ctx context.Context) error {
	if p.cancel == nil {
		return nil
	}

	p.cancel()

	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop Athenz policy refresh: %w", ctx.Err())
	}
}

func (p *policyProcessor) SecurityRequirements() processor.SecurityRequirements {
	return processor.SecurityRequirements{
		ResolvesTenant: false, NeedsTenant: false,
		RegistersEnforcement: p.config.EnforceLabel != "", AppliesEnforcement: false,
	}
}

func (p *policyProcessor) ProcessQuery(ctx context.Context, query *qdata.Query) error {
	identity, ok := qdata.IdentityFromContext(ctx)
	if !ok || p.allows == nil || !p.allows(identity) || len(identity.Roles) == 0 {
		return qerror.New(qerror.CodePermissionDenied, "verified Athenz roles required")
	}

	if time.Since(time.Unix(0, p.lastRefresh.Load())) > p.config.MaxPolicyAge {
		return qerror.New(qerror.CodeUnavailable, "Athenz policy unavailable")
	}

	err := p.checkResources(ctx, query, identity.Roles)
	if err != nil {
		return err
	}

	if p.config.EnforceLabel != "" {
		query.EnforcedMatchers = append(query.EnforcedMatchers, &qdata.LabelMatcher{
			Name: p.config.EnforceLabel, Op: qdata.MatchEqual, Value: identity.Tenant,
		})
	}

	return nil
}

func (p *policyProcessor) refresh(ctx context.Context, update func(context.Context) error) {
	defer close(p.done)

	ticker := time.NewTicker(p.config.RefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := update(ctx)
			if err != nil {
				slog.Warn("Athenz policy refresh failed", "domain", p.domain)

				continue
			}

			p.lastRefresh.Store(time.Now().UnixNano())
		}
	}
}

func (p *policyProcessor) checkResources(ctx context.Context, query *qdata.Query, roles []string) error {
	signals := qdata.QuerySignals(query)
	if len(signals) == 0 {
		return qerror.New(qerror.CodePermissionDenied, "missing Athenz query resource")
	}

	for _, signal := range signals {
		if signal == qdata.SignalUnspecified {
			return qerror.New(qerror.CodePermissionDenied, "missing Athenz query resource")
		}

		kind := strings.ToLower(strings.TrimPrefix(signal.String(), "SIGNAL_"))
		resource := p.domain + ":" + p.config.ResourcePrefix + "." + kind

		err := p.check(ctx, p.domain, roles, p.config.Action, resource)
		if err != nil {
			if ctx.Err() != nil {
				return qerror.UpstreamTransportError(ctx.Err())
			}

			return qerror.New(qerror.CodePermissionDenied, "Athenz policy denied query")
		}
	}

	return nil
}
