package cacheprocessor

import (
	"context"
	"errors"
	"fmt"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/processor"
)

var errInvalidConfig = errors.New("cache: invalid config type")

// NewFactory returns the cache processor factory.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType("cache"),
		func() component.Config { return new(Config) },
		createProcessor,
	)
}

func createProcessor(_ context.Context, _ component.Settings, cfg component.Config) (processor.Processor, error) {
	config, ok := cfg.(*Config)
	if !ok {
		return nil, errInvalidConfig
	}

	proc, err := New(*config)
	if err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}

	return proc, nil
}
