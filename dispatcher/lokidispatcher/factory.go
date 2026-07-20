package lokidispatcher

import (
	"context"
	"errors"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
)

// errInvalidConfig is returned when the factory receives an unexpected config type.
var errInvalidConfig = errors.New("lokidispatcher: invalid config type")

// NewFactory returns the factory for the Loki dispatcher.
func NewFactory() dispatcher.Factory {
	return dispatcher.NewFactory(
		component.MustNewType("loki"),
		createDefaultConfig,
		createDispatcher,
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		Endpoint:     DefaultEndpoint,
		TenantHeader: DefaultTenantHeader,
		Timeout:      defaultTimeout,
		Limit:        DefaultLimit,
		Direction:    DefaultDirection,
	}
}

func createDispatcher(_ context.Context, _ component.Settings, cfg component.Config) (dispatcher.Dispatcher, error) {
	conf, ok := cfg.(*Config)
	if !ok {
		return nil, errInvalidConfig
	}

	return New(*conf), nil
}
