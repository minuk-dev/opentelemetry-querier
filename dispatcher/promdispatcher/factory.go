package promdispatcher

import (
	"context"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
)

// Type is the component type produced by this factory.
var Type = component.MustNewType("prometheus")

// NewFactory returns the factory for the Prometheus dispatcher.
func NewFactory() dispatcher.Factory {
	return dispatcher.NewFactory(Type, createDefaultConfig, createDispatcher)
}

func createDefaultConfig() component.Config {
	return &Config{
		Endpoint:     "http://localhost:9090",
		TenantHeader: DefaultTenantHeader,
		Timeout:      30 * time.Second,
	}
}

func createDispatcher(_ context.Context, _ component.Settings, cfg component.Config) (dispatcher.Dispatcher, error) {
	return New(*cfg.(*Config)), nil
}
