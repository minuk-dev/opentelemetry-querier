package prometheusdispatcher

import (
	"context"
	"errors"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
)

// errInvalidConfig is returned when the factory receives an unexpected config type.
var errInvalidConfig = errors.New("promdispatcher: invalid config type")

var errResultLimits = errors.New("result counts must be non-negative and max_response_bytes must not exceed 16 MiB")

// NewFactory returns the factory for the Prometheus dispatcher.
func NewFactory() dispatcher.Factory {
	return dispatcher.NewFactory(
		component.MustNewType("prometheus"),
		createDefaultConfig,
		createDispatcher,
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		MaxResponseBytes: 0, MaxSeries: 0, MaxSamples: 0,
		Endpoint:     "http://localhost:9090",
		TenantHeader: DefaultTenantHeader,
		Timeout:      defaultTimeout,
	}
}

func createDispatcher(_ context.Context, _ component.Settings, cfg component.Config) (dispatcher.Dispatcher, error) {
	conf, ok := cfg.(*Config)
	if !ok {
		return nil, errInvalidConfig
	}

	if conf.MaxSeries < 0 || conf.MaxSamples < 0 || conf.MaxResponseBytes < 0 || conf.MaxResponseBytes > maxResponseBytes {
		return nil, errResultLimits
	}

	return New(*conf), nil
}
