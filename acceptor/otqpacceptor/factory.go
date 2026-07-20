package otqpacceptor

import (
	"context"
	"errors"

	"github.com/minuk-dev/opentelemetry-querier/acceptor"
	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
)

// errInvalidConfig is returned when the factory receives an unexpected config type.
var errInvalidConfig = errors.New("otqp: invalid config type")

// NewFactory returns the factory for the OTQP acceptor. A builder-generated
// distribution registers this to make "otqp" available in configs.
func NewFactory() acceptor.Factory {
	return acceptor.NewFactory(
		component.MustNewType("otqp"),
		createDefaultConfig,
		createAcceptor,
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		GRPCEndpoint: DefaultGRPCEndpoint,
		HTTPEndpoint: DefaultHTTPEndpoint,
	}
}

func createAcceptor(
	_ context.Context,
	_ component.Settings,
	cfg component.Config,
	next pipeline.Handler,
) (acceptor.Acceptor, error) {
	conf, ok := cfg.(*Config)
	if !ok {
		return nil, errInvalidConfig
	}

	return New(*conf, next), nil
}
