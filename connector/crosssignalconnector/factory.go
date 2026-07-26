package crosssignalconnector

import (
	"context"
	"errors"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/connector"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// errInvalidConfig is returned when the factory receives an unexpected config type.
var errInvalidConfig = errors.New("crosssignalconnector: invalid config type")

// Config configures the cross-signal connector. It currently has no options; the
// join mode and keys are derived from the plan's BinaryOp and vector matching.
type Config struct{}

// NewFactory returns the factory for the cross-signal connector.
func NewFactory() connector.Factory {
	return connector.NewFactory(
		component.MustNewType("crosssignal"),
		createDefaultConfig,
		createConnector,
	)
}

func createDefaultConfig() component.Config {
	return &Config{}
}

func createConnector(
	_ context.Context,
	_ component.Settings,
	cfg component.Config,
	targets map[qdata.Signal]pipeline.Handler,
) (connector.Connector, error) {
	_, ok := cfg.(*Config)
	if !ok {
		return nil, errInvalidConfig
	}

	return New(targets), nil
}
