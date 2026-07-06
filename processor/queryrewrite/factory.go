package queryrewrite

import (
	"context"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/processor"
)

// Type is the component type produced by this factory.
var Type = component.MustNewType("queryrewrite")

// NewFactory returns the factory for the query-rewrite processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(Type, createDefaultConfig, createProcessor)
}

func createDefaultConfig() component.Config {
	return &Config{}
}

func createProcessor(_ context.Context, _ component.Settings, cfg component.Config) (processor.Processor, error) {
	return New(*cfg.(*Config)), nil
}
