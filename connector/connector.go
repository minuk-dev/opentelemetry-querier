// Package connector defines the Connector component category and its Factory,
// mirroring go.opentelemetry.io/collector/connector. A Connector is a pipeline
// terminal (it satisfies dispatcher.Dispatcher) that, instead of hitting storage,
// bridges pipelines: it fans a query out to per-signal target pipelines and
// combines their results. This is how a cross-signal query is executed without a
// unified backend — each single-signal subtree runs through its own pipeline
// (processors and all), and the connector joins the results.
package connector

import (
	"context"
	"errors"
	"fmt"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// errDuplicateFactory is returned when two factories share a type.
var errDuplicateFactory = errors.New("connector: duplicate factory type")

// Connector is a pipeline terminal that bridges to other pipelines. It is a
// dispatcher.Dispatcher so it can occupy a pipeline's terminal slot.
type Connector interface {
	dispatcher.Dispatcher
}

// Factory creates Connectors of a single type (cf. connector.Factory).
type Factory interface {
	component.Factory

	// CreateConnector builds a connector wired to the per-signal target pipelines
	// it fans queries out to.
	CreateConnector(
		ctx context.Context,
		set component.Settings,
		cfg component.Config,
		targets map[qdata.Signal]pipeline.Handler,
	) (Connector, error)
}

// CreateConnectorFunc is the function form of Factory.CreateConnector.
type CreateConnectorFunc func(
	ctx context.Context,
	set component.Settings,
	cfg component.Config,
	targets map[qdata.Signal]pipeline.Handler,
) (Connector, error)

type factory struct {
	typ           component.Type
	defaultConfig func() component.Config
	createFunc    CreateConnectorFunc
}

func (f *factory) Type() component.Type                  { return f.typ }
func (f *factory) CreateDefaultConfig() component.Config { return f.defaultConfig() }

func (f *factory) CreateConnector(
	ctx context.Context,
	set component.Settings,
	cfg component.Config,
	targets map[qdata.Signal]pipeline.Handler,
) (Connector, error) {
	return f.createFunc(ctx, set, cfg, targets)
}

// NewFactory assembles a connector Factory.
func NewFactory(typ component.Type, defaultConfig func() component.Config, create CreateConnectorFunc) Factory {
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
