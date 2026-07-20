package queryrewriteprocessor

import (
	"context"
	"errors"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/processor"
)

// errInvalidConfig is returned when the factory receives an unexpected config type.
var errInvalidConfig = errors.New("queryrewrite: invalid config type")

// NewFactory returns the factory for the query-rewrite processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType("queryrewrite"),
		createDefaultConfig,
		createProcessor,
	)
}

func createDefaultConfig() component.Config {
	return &Config{EnforceLabels: nil}
}

func createProcessor(_ context.Context, _ component.Settings, cfg component.Config) (processor.Processor, error) {
	conf, ok := cfg.(*Config)
	if !ok {
		return nil, errInvalidConfig
	}

	return New(*conf), nil
}
