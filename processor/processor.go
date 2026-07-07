// Package processor defines the Processor component category and its Factory,
// mirroring go.opentelemetry.io/collector/processor. A Processor is a pipeline
// stage that may transform a query on the way in, transform the result on the
// way out, or short-circuit the request (auth, rate limit).
package processor

import (
	"context"
	"errors"
	"fmt"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// errDuplicateFactory is returned when two factories share a type.
var errDuplicateFactory = errors.New("processor: duplicate factory type")

// Processor is a single pipeline stage. The pipeline calls ProcessQuery on every
// processor in order on the request path, then (after the dispatcher runs)
// ProcessResult on every processor in reverse order on the response path. It is
// also a component.Component for lifecycle.
//
// Returning an error from ProcessQuery short-circuits the pipeline before the
// dispatcher is reached; return a *qerror.Error to control the transport status.
type Processor interface {
	component.Component

	// ProcessQuery transforms or validates the query on the request path.
	ProcessQuery(ctx context.Context, q *qdata.Query) error

	// ProcessResult transforms the result on the response path. It receives the
	// (already mutated) query for context and may attach feedback notifications.
	ProcessResult(ctx context.Context, q *qdata.Query, r *qdata.Result) error
}

// Base provides no-op implementations of every Processor method so a concrete
// processor can embed it and override only what it needs.
type Base struct{}

// Start is a no-op.
func (Base) Start(context.Context, component.Host) error { return nil }

// Shutdown is a no-op.
func (Base) Shutdown(context.Context) error { return nil }

// ProcessQuery is a no-op.
func (Base) ProcessQuery(context.Context, *qdata.Query) error { return nil }

// ProcessResult is a no-op.
func (Base) ProcessResult(context.Context, *qdata.Query, *qdata.Result) error { return nil }

// Factory creates Processors of a single type (cf. processor.Factory).
type Factory interface {
	component.Factory

	// CreateProcessor builds a processor instance from its config.
	CreateProcessor(ctx context.Context, set component.Settings, cfg component.Config) (Processor, error)
}

// CreateProcessorFunc is the function form of Factory.CreateProcessor.
type CreateProcessorFunc func(
	ctx context.Context,
	set component.Settings,
	cfg component.Config,
) (Processor, error)

type factory struct {
	typ           component.Type
	defaultConfig func() component.Config
	createFunc    CreateProcessorFunc
}

func (f *factory) Type() component.Type                  { return f.typ }
func (f *factory) CreateDefaultConfig() component.Config { return f.defaultConfig() }

func (f *factory) CreateProcessor(
	ctx context.Context,
	set component.Settings,
	cfg component.Config,
) (Processor, error) {
	return f.createFunc(ctx, set, cfg)
}

// NewFactory assembles a processor Factory.
func NewFactory(typ component.Type, defaultConfig func() component.Config, create CreateProcessorFunc) Factory {
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
