package lokidispatcher

import (
	"context"
	"errors"
	"fmt"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
)

var (
	// errInvalidConfig is returned when the factory receives an unexpected config type.
	errInvalidConfig = errors.New("lokidispatcher: invalid config type")
	// errInvalidDirection is returned for a direction other than forward/backward.
	errInvalidDirection = errors.New("lokidispatcher: direction must be \"forward\" or \"backward\"")
	// errNegativeLimit is returned for a negative limit.
	errNegativeLimit = errors.New("lokidispatcher: limit must not be negative")
)

// Scan directions accepted by Loki.
const (
	directionForward  = "forward"
	directionBackward = "backward"
)

// NewFactory returns the factory for the Loki dispatcher.
func NewFactory() dispatcher.Factory {
	return dispatcher.NewFactory(
		component.MustNewType("loki"),
		createDefaultConfig,
		createDispatcher,
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		Endpoint:     DefaultEndpoint,
		TenantHeader: DefaultTenantHeader,
		Timeout:      defaultTimeout,
		Limit:        DefaultLimit,
		Direction:    DefaultDirection,
	}
}

func createDispatcher(_ context.Context, _ component.Settings, cfg component.Config) (dispatcher.Dispatcher, error) {
	conf, ok := cfg.(*Config)
	if !ok {
		return nil, errInvalidConfig
	}

	err := Validate(*conf)
	if err != nil {
		return nil, err
	}

	return New(*conf), nil
}

// Validate rejects a config that would make every query fail at runtime: an
// unrecognized scan direction, or a negative limit. It is called by the factory
// so misconfiguration fails at startup, not per query. An empty direction is
// allowed (New defaults it).
func Validate(cfg Config) error {
	switch cfg.Direction {
	case "", directionForward, directionBackward:
	default:
		return fmt.Errorf("%w: got %q", errInvalidDirection, cfg.Direction)
	}

	if cfg.Limit < 0 {
		return fmt.Errorf("%w: got %d", errNegativeLimit, cfg.Limit)
	}

	return nil
}
