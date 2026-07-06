// Package acceptor defines the Acceptor component category and its Factory,
// mirroring go.opentelemetry.io/collector/receiver. An Acceptor accepts queries
// from clients over some transport and feeds them to the pipeline (its "next
// consumer"), then serializes the result back. The default acceptor speaks OTQP;
// alternatives can speak backend-native query APIs.
package acceptor

import (
	"context"
	"fmt"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
)

// Acceptor is a component that accepts client queries. It is a component.Component
// (Start binds listeners, Shutdown stops them).
type Acceptor interface {
	component.Component
}

// Factory creates Acceptors of a single type (cf. receiver.Factory).
type Factory interface {
	component.Factory

	// CreateAcceptor builds an acceptor instance that forwards accepted queries
	// to next.
	CreateAcceptor(ctx context.Context, set component.Settings, cfg component.Config, next pipeline.Handler) (Acceptor, error)
}

// CreateAcceptorFunc is the function form of Factory.CreateAcceptor.
type CreateAcceptorFunc func(ctx context.Context, set component.Settings, cfg component.Config, next pipeline.Handler) (Acceptor, error)

type factory struct {
	component.BaseFactory
	typ           component.Type
	defaultConfig func() component.Config
	createFunc    CreateAcceptorFunc
}

func (f *factory) Type() component.Type                  { return f.typ }
func (f *factory) CreateDefaultConfig() component.Config { return f.defaultConfig() }

func (f *factory) CreateAcceptor(ctx context.Context, set component.Settings, cfg component.Config, next pipeline.Handler) (Acceptor, error) {
	return f.createFunc(ctx, set, cfg, next)
}

// NewFactory assembles an acceptor Factory from its type, default-config
// constructor and create function.
func NewFactory(typ component.Type, defaultConfig func() component.Config, create CreateAcceptorFunc) Factory {
	return &factory{typ: typ, defaultConfig: defaultConfig, createFunc: create}
}

// MakeFactoryMap indexes factories by type, erroring on duplicates. A
// builder-generated distribution uses this to populate its factory set.
func MakeFactoryMap(factories ...Factory) (map[component.Type]Factory, error) {
	out := make(map[component.Type]Factory, len(factories))
	for _, f := range factories {
		if _, dup := out[f.Type()]; dup {
			return nil, fmt.Errorf("duplicate acceptor factory %q", f.Type())
		}
		out[f.Type()] = f
	}
	return out, nil
}
