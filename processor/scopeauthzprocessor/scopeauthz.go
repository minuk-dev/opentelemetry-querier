// Package scopeauthzprocessor requires scopes on the verified request identity.
package scopeauthzprocessor

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// Config declares the issuer and the scopes required for every query.
type Config struct {
	Issuer         string   `mapstructure:"issuer"`
	RequiredScopes []string `mapstructure:"required_scopes"`
}

type scopeProcessor struct {
	processor.Base

	config Config
}

var errConfig = errors.New("scopeauthz: issuer and nonempty required scopes are required")

// NewFactory registers the verified-scope policy processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType("scopeauthz"), func() component.Config { return new(Config) }, create,
	)
}

func create(_ context.Context, _ component.Settings, cfg component.Config) (processor.Processor, error) {
	config, ok := cfg.(*Config)
	if !ok || config.Issuer == "" || len(config.RequiredScopes) == 0 {
		return nil, errConfig
	}

	for _, scope := range config.RequiredScopes {
		if scope == "" || strings.ContainsAny(scope, " \t\r\n") {
			return nil, errConfig
		}
	}

	return &scopeProcessor{Base: processor.Base{}, config: *config}, nil
}

func (p *scopeProcessor) ProcessQuery(ctx context.Context, _ *qdata.Query) error {
	identity, verified := qdata.IdentityFromContext(ctx)
	if !verified || identity.Authority != p.config.Issuer {
		return qerror.New(qerror.CodePermissionDenied, "scopeauthz: verified issuer required")
	}

	for _, scope := range p.config.RequiredScopes {
		if !slices.Contains(identity.Scopes, scope) {
			return qerror.New(qerror.CodePermissionDenied, "scopeauthz: required scope missing")
		}
	}

	return nil
}
