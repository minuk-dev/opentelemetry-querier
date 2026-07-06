package otqp

import (
	"context"

	"github.com/minuk-dev/opentelemetry-querier/acceptor"
	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
)

// Type is the component type produced by this factory.
var Type = component.MustNewType("otqp")

// NewFactory returns the factory for the OTQP acceptor. A builder-generated
// distribution registers this to make "otqp" available in configs.
func NewFactory() acceptor.Factory {
	return acceptor.NewFactory(Type, createDefaultConfig, createAcceptor)
}

func createDefaultConfig() component.Config {
	return &Config{
		GRPCEndpoint: DefaultGRPCEndpoint,
		HTTPEndpoint: DefaultHTTPEndpoint,
	}
}

func createAcceptor(_ context.Context, _ component.Settings, cfg component.Config, next pipeline.Handler) (acceptor.Acceptor, error) {
	return New(*cfg.(*Config), next), nil
}
