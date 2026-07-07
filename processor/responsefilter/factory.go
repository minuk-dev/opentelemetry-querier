package responsefilter

import (
	"context"
	"errors"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/processor"
)

// errInvalidConfig is returned when the factory receives an unexpected config type.
var errInvalidConfig = errors.New("responsefilter: invalid config type")

// NewFactory returns the factory for the response-filter processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType(typeStr),
		createDefaultConfig,
		createProcessor,
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		DropLabels:             nil,
		MaskLabels:             nil,
		MaskWith:               "***",
		WarnCounterWithoutRate: false,
	}
}

func createProcessor(_ context.Context, _ component.Settings, cfg component.Config) (processor.Processor, error) {
	conf, ok := cfg.(*Config)
	if !ok {
		return nil, errInvalidConfig
	}

	return New(*conf), nil
}
