package elasticsearchacceptor

import (
	"context"
	"errors"

	"github.com/minuk-dev/opentelemetry-querier/acceptor"
	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
)

// errInvalidConfig is returned when the factory receives an unexpected config type.
var errInvalidConfig = errors.New("elasticsearchacceptor: invalid config type")

// NewFactory returns the factory for the Elasticsearch _search API acceptor.
func NewFactory() acceptor.Factory {
	return acceptor.NewFactory(
		component.MustNewType("elasticsearch"),
		createDefaultConfig,
		createAcceptor,
	)
}

func createDefaultConfig() component.Config {
	return &Config{Endpoint: DefaultEndpoint}
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
