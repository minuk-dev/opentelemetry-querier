// Package extension defines independently configurable service components.
package extension

import (
	"context"
	"errors"
	"fmt"

	"github.com/minuk-dev/opentelemetry-querier/component"
)

// errDuplicateFactory is returned when two factories share a type.
var errDuplicateFactory = errors.New("extension: duplicate factory type")

// Extension is an optional service component with its own lifecycle.
type Extension interface {
	component.Component
}

// Factory creates extensions of a single type (cf. receiver.Factory).
type Factory interface {
	component.Factory

	// CreateExtension builds a service extension.
	CreateExtension(
		ctx context.Context,
		set component.Settings,
		cfg component.Config,
	) (Extension, error)
}

// CreateExtensionFunc is the function form of Factory.CreateExtension.
type CreateExtensionFunc func(
	ctx context.Context,
	set component.Settings,
	cfg component.Config,
) (Extension, error)

type factory struct {
	typ           component.Type
	defaultConfig func() component.Config
	createFunc    CreateExtensionFunc
}

func (f *factory) Type() component.Type                  { return f.typ }
func (f *factory) CreateDefaultConfig() component.Config { return f.defaultConfig() }

func (f *factory) CreateExtension(
	ctx context.Context,
	set component.Settings,
	cfg component.Config,
) (Extension, error) {
	return f.createFunc(ctx, set, cfg)
}

// NewFactory assembles an extension Factory from its type, default-config
// constructor and create function.
func NewFactory(typ component.Type, defaultConfig func() component.Config, create CreateExtensionFunc) Factory {
	return &factory{typ: typ, defaultConfig: defaultConfig, createFunc: create}
}

// MakeFactoryMap indexes factories by type, erroring on duplicates. A
// builder-generated distribution uses this to populate its factory set.
func MakeFactoryMap(factories ...Factory) (map[component.Type]Factory, error) {
	out := make(map[component.Type]Factory, len(factories))

	for _, f := range factories {
		if _, dup := out[f.Type()]; dup {
			return nil, fmt.Errorf("%w %q", errDuplicateFactory, f.Type())
		}

		out[f.Type()] = f
	}

	return out, nil
}
