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
var errMaxRows = errors.New("crosssignalconnector max_rows must be between 0 and 10000")

var errInvalidConfig = errors.New("crosssignalconnector: invalid config type")

// Config configures cross-signal result size.
type Config struct {
	MaxRows int `mapstructure:"max_rows"`
}

// NewFactory returns the factory for the cross-signal connector.
func NewFactory() connector.Factory {
	return connector.NewFactory(
		component.MustNewType("crosssignal"),
		createDefaultConfig,
		createConnector,
	)
}

func createDefaultConfig() component.Config {
	return &Config{MaxRows: 0}
}

func createConnector(
	_ context.Context,
	_ component.Settings,
	cfg component.Config,
	targets map[qdata.Signal]pipeline.Handler,
) (connector.Connector, error) {
	conf, ok := cfg.(*Config)
	if !ok {
		return nil, errInvalidConfig
	}

	if conf.MaxRows < 0 || conf.MaxRows > maxJoinRows {
		return nil, errMaxRows
	}

	connector := New(targets)
	connector.maxRows = conf.MaxRows

	return connector, nil
}
