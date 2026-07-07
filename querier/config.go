package querier

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-viper/mapstructure/v2"
	"gopkg.in/yaml.v3"

	"github.com/minuk-dev/opentelemetry-querier/component"
)

var (
	// errEmptyPipelines is returned when no pipelines are configured.
	errEmptyPipelines = errors.New("querier: service.pipelines is empty")
	// errNoAcceptors is returned when a pipeline lists no acceptors.
	errNoAcceptors = errors.New("querier: pipeline has no acceptors")
	// errDispatcherCount is returned when a pipeline does not list exactly one dispatcher.
	errDispatcherCount = errors.New("querier: pipeline must list exactly one dispatcher")
)

// Config is the runtime configuration, mirroring the collector layout: component
// instances are declared under acceptors / processors / dispatchers keyed by
// component ID ("type" or "type/name"), and wired into named pipelines under
// service. Each component's settings are captured as a raw map and decoded
// against its factory's default config during Build.
//
// Like the collector's confmap, the config is loaded by parsing YAML into an
// untyped tree and then structurally decoding it with mapstructure, so keys use
// mapstructure tags (snake_case) throughout.
type Config struct {
	Acceptors   map[string]map[string]any `mapstructure:"acceptors"`
	Processors  map[string]map[string]any `mapstructure:"processors"`
	Dispatchers map[string]map[string]any `mapstructure:"dispatchers"`
	Service     ServiceConfig             `mapstructure:"service"`
}

// ServiceConfig lists the pipelines to run, keyed by pipeline ID
// (e.g. "query/default").
type ServiceConfig struct {
	Pipelines map[string]PipelineConfig `mapstructure:"pipelines"`
}

// PipelineConfig references component instances by ID, defining processor order.
type PipelineConfig struct {
	Acceptors   []string `mapstructure:"acceptors"`
	Processors  []string `mapstructure:"processors"`
	Dispatchers []string `mapstructure:"dispatchers"`
}

// LoadConfig reads a YAML config file and structurally decodes it.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("querier: read config %s: %w", path, err)
	}

	var tree map[string]any

	err = yaml.Unmarshal(data, &tree)
	if err != nil {
		return nil, fmt.Errorf("querier: parse config %s: %w", path, err)
	}

	cfg := &Config{
		Acceptors:   nil,
		Processors:  nil,
		Dispatchers: nil,
		Service:     ServiceConfig{Pipelines: nil},
	}

	err = decodeStrict(tree, cfg)
	if err != nil {
		return nil, fmt.Errorf("querier: decode config %s: %w", path, err)
	}

	err = cfg.validate()
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) validate() error {
	if len(c.Service.Pipelines) == 0 {
		return errEmptyPipelines
	}

	for name, pipe := range c.Service.Pipelines {
		if len(pipe.Acceptors) == 0 {
			return fmt.Errorf("%w: %q", errNoAcceptors, name)
		}

		if len(pipe.Dispatchers) != 1 {
			return fmt.Errorf("%w: %q", errDispatcherCount, name)
		}
	}

	return nil
}

// decodeStrict decodes an untyped tree into out with mapstructure, rejecting
// unknown keys and converting duration strings (e.g. "30s").
func decodeStrict(input, out any) error {
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:      out,
		ErrorUnused: true,
		TagName:     "mapstructure",
		DecodeHook:  mapstructure.StringToTimeDurationHookFunc(),
	})
	if err != nil {
		return fmt.Errorf("querier: build decoder: %w", err)
	}

	err = decoder.Decode(input)
	if err != nil {
		return fmt.Errorf("querier: decode: %w", err)
	}

	return nil
}

// parseID parses a component ID string ("type" or "type/name").
func parseID(idStr string) (component.ID, error) {
	var id component.ID

	err := id.UnmarshalText([]byte(idStr))
	if err != nil {
		return component.ID{}, fmt.Errorf("querier: %w", err)
	}

	return id, nil
}
