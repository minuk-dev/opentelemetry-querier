// Package pipeline wires an acceptor to a dispatcher through an ordered chain of
// processors, mirroring the opentelemetry-collector receiver→processor→exporter
// pipeline but for queries: Acceptor → [Processors] → Dispatcher → storage, with
// results flowing back out through the processors in reverse.
package pipeline

import (
	"context"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// Handler evaluates a query end to end. Acceptors depend on this interface
// rather than the concrete Pipeline so they can be tested with a stub.
type Handler interface {
	Handle(ctx context.Context, q *qdata.Query) (*qdata.Result, error)
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ctx context.Context, q *qdata.Query) (*qdata.Result, error)

func (f HandlerFunc) Handle(ctx context.Context, q *qdata.Query) (*qdata.Result, error) {
	return f(ctx, q)
}

// Pipeline is an ordered processor chain terminated by a dispatcher.
type Pipeline struct {
	Name       string
	Processors []processor.Processor
	Dispatcher dispatcher.Dispatcher
}

// New builds a pipeline.
func New(name string, processors []processor.Processor, disp dispatcher.Dispatcher) *Pipeline {
	return &Pipeline{Name: name, Processors: processors, Dispatcher: disp}
}

// Handle runs the request path (processors in order), dispatches to storage,
// then runs the response path (processors in reverse order). A processor error
// on the request path short-circuits before the dispatcher is reached.
func (p *Pipeline) Handle(ctx context.Context, q *qdata.Query) (*qdata.Result, error) {
	for _, proc := range p.Processors {
		if err := proc.ProcessQuery(ctx, q); err != nil {
			return nil, err
		}
	}

	result, err := p.Dispatcher.Dispatch(ctx, q)
	if err != nil {
		return nil, err
	}
	if result.GetSignal() == qdata.SignalUnspecified {
		result.Signal = q.GetSignal()
	}

	for i := len(p.Processors) - 1; i >= 0; i-- {
		if err := p.Processors[i].ProcessResult(ctx, q, result); err != nil {
			return nil, err
		}
	}
	return result, nil
}

// Start starts the processor chain and the dispatcher (acceptors are started
// separately since they call back into Handle). Components are started in
// dispatcher-to-front order so downstream is ready before upstream.
func (p *Pipeline) Start(ctx context.Context, host component.Host) error {
	if err := p.Dispatcher.Start(ctx, host); err != nil {
		return err
	}
	for i := len(p.Processors) - 1; i >= 0; i-- {
		if err := p.Processors[i].Start(ctx, host); err != nil {
			return err
		}
	}
	return nil
}

// Shutdown stops the processor chain and dispatcher in front-to-dispatcher order.
func (p *Pipeline) Shutdown(ctx context.Context) error {
	for _, proc := range p.Processors {
		_ = proc.Shutdown(ctx)
	}
	return p.Dispatcher.Shutdown(ctx)
}
