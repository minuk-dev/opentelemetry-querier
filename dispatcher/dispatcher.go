// Package dispatcher defines the Dispatcher component category and its Factory,
// mirroring go.opentelemetry.io/collector/exporter. A Dispatcher is the terminal
// pipeline stage that renders a qdata Query to a concrete storage backend,
// executes it, and parses the backend response back into a qdata Result.
package dispatcher

import (
	"context"
	"errors"
	"fmt"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// errDuplicateFactory is returned when two factories share a type.
var errDuplicateFactory = errors.New("dispatcher: duplicate factory type")

// Dispatcher sends a query to storage and returns the result. It is also a
// component.Component for lifecycle (opening/closing backend clients).
type Dispatcher interface {
	component.Component

	// Dispatch renders q to the backend, executes it, and returns the result.
	// The returned Result's Signal should match q.Signal.
	Dispatch(ctx context.Context, q *qdata.Query) (*qdata.Result, error)
}

// Base provides no-op lifecycle methods for dispatchers that need no setup.
type Base struct{}

// Start is a no-op.
func (Base) Start(context.Context, component.Host) error { return nil }

// Shutdown is a no-op.
func (Base) Shutdown(context.Context) error { return nil }

// Factory creates Dispatchers of a single type (cf. exporter.Factory).
type Factory interface {
	component.Factory

	// CreateDispatcher builds a dispatcher instance from its config.
	CreateDispatcher(ctx context.Context, set component.Settings, cfg component.Config) (Dispatcher, error)
}

// CreateDispatcherFunc is the function form of Factory.CreateDispatcher.
type CreateDispatcherFunc func(
	ctx context.Context,
	set component.Settings,
	cfg component.Config,
) (Dispatcher, error)

type factory struct {
	typ           component.Type
	defaultConfig func() component.Config
	createFunc    CreateDispatcherFunc
}

func (f *factory) Type() component.Type                  { return f.typ }
func (f *factory) CreateDefaultConfig() component.Config { return f.defaultConfig() }

func (f *factory) CreateDispatcher(
	ctx context.Context,
	set component.Settings,
	cfg component.Config,
) (Dispatcher, error) {
	return f.createFunc(ctx, set, cfg)
}

// NewFactory assembles a dispatcher Factory.
func NewFactory(typ component.Type, defaultConfig func() component.Config, create CreateDispatcherFunc) Factory {
	return &factory{typ: typ, defaultConfig: defaultConfig, createFunc: create}
}

// MakeFactoryMap indexes factories by type, erroring on duplicates.
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
