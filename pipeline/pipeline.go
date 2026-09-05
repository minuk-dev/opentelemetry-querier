// Package pipeline wires an acceptor to a dispatcher through an ordered chain of
// processors, mirroring the opentelemetry-collector receiver→processor→exporter
// pipeline but for queries: Acceptor → [Processors] → Dispatcher → storage, with
// results flowing back out through the processors in reverse.
package pipeline

import (
	"context"
	"fmt"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// Handler evaluates a query end to end. Acceptors depend on this interface
// rather than the concrete Pipeline so they can be tested with a stub.
type Handler interface {
	Handle(ctx context.Context, q *qdata.Query) (*qdata.Result, error)
}

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(ctx context.Context, q *qdata.Query) (*qdata.Result, error)

// Handle calls the wrapped function.
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

// Handle validates the client's plan, runs the request path (processors in
// order), dispatches to storage, then runs the response path (processors in
// reverse order). A validation or processor error on the request path
// short-circuits before the dispatcher is reached.
func (p *Pipeline) Handle(ctx context.Context, query *qdata.Query) (*qdata.Result, error) {
	err := p.validatePlan(query)
	if err != nil {
		return nil, err
	}

	// Mirror the plan's signal set onto Query.signals for downstream readers
	// (no-op without a plan).
	qdata.SyncPlanSignals(query)

	for _, proc := range p.Processors {
		err = proc.ProcessQuery(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("pipeline %q: %w", p.Name, err)
		}
	}

	result, err := p.Dispatcher.Dispatch(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("pipeline %q: %w", p.Name, err)
	}

	// Backfill the single-signal convenience field from the query when a
	// single-signal dispatcher left it unset. A relational Table (cross-signal)
	// result is legitimately signal-unspecified, so it is left untouched.
	if result.GetSignal() == qdata.SignalUnspecified && result.GetTable() == nil {
		result.Signal = query.GetSignal()
	}

	for i := len(p.Processors) - 1; i >= 0; i-- {
		err = p.Processors[i].ProcessResult(ctx, query, result)
		if err != nil {
			return nil, fmt.Errorf("pipeline %q: %w", p.Name, err)
		}
	}

	return result, nil
}

// Start starts the processor chain and the dispatcher (acceptors are started
// separately since they call back into Handle). Components are started in
// dispatcher-to-front order so downstream is ready before upstream.
func (p *Pipeline) Start(ctx context.Context, host component.Host) error {
	err := p.Dispatcher.Start(ctx, host)
	if err != nil {
		return fmt.Errorf("pipeline %q: start dispatcher: %w", p.Name, err)
	}

	for i := len(p.Processors) - 1; i >= 0; i-- {
		err := p.Processors[i].Start(ctx, host)
		if err != nil {
			return fmt.Errorf("pipeline %q: start processor: %w", p.Name, err)
		}
	}

	return nil
}

// Shutdown stops the processor chain and dispatcher in front-to-dispatcher order.
func (p *Pipeline) Shutdown(ctx context.Context) error {
	for _, proc := range p.Processors {
		_ = proc.Shutdown(ctx)
	}

	err := p.Dispatcher.Shutdown(ctx)
	if err != nil {
		return fmt.Errorf("pipeline %q: shutdown dispatcher: %w", p.Name, err)
	}

	return nil
}

// validatePlan checks the client-supplied plan for structural well-formedness
// before any processor or dispatcher sees it (issue #67). The pipeline is the
// one place every acceptor funnels through, so validating here means a
// malformed plan is rejected once, as CodeInvalidArgument, instead of each
// dispatcher re-deriving well-formedness while rendering — where a zero
// time_agg window became an upstream error, an aggregate setting both by and
// without silently lost its without, and an empty function name rendered as
// "(...)".
//
// A query that carries no plan at all is left alone: that is still a shape the
// pipeline itself handles (see SyncPlanSignals), and every dispatcher already
// fails closed on it with its own message.
func (p *Pipeline) validatePlan(query *qdata.Query) error {
	plan := query.GetPlan()
	if plan == nil {
		return nil
	}

	err := qdata.ValidatePlan(plan)
	if err != nil {
		return qerror.New(qerror.CodeInvalidArgument, "pipeline %q: invalid query plan: %v", p.Name, err)
	}

	return nil
}
