// Package pipeline wires an acceptor to a dispatcher through an ordered chain of
// processors, mirroring the opentelemetry-collector receiver→processor→exporter
// pipeline but for queries: Acceptor → [Processors] → Dispatcher → storage, with
// results flowing back out through the processors in reverse.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

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

type quotaAdmittedKey struct{}

// Pipeline is an ordered processor chain terminated by a dispatcher.
type Pipeline struct {
	AdmitQuota func(context.Context) error
	Name       string
	// Timeout bounds the entire query. A zero value uses the caller deadline.
	Timeout    time.Duration
	Processors []processor.Processor
	Dispatcher dispatcher.Dispatcher
}

// New builds a pipeline.
func New(name string, processors []processor.Processor, disp dispatcher.Dispatcher) *Pipeline {
	return &Pipeline{Name: name, Timeout: 0, AdmitQuota: nil, Processors: processors, Dispatcher: disp}
}

// Handle validates the client's plan, runs the request path (processors in
// order), dispatches to storage, then runs the response path (processors in
// reverse order). A validation or processor error on the request path
// short-circuits before the dispatcher is reached.
func (p *Pipeline) Handle(ctx context.Context, query *qdata.Query) (*qdata.Result, error) {
	if p.Timeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, p.Timeout)
		defer cancel()
	}

	result, err := p.handle(ctx, query)
	if ctx.Err() != nil {
		return nil, queryContextError(ctx)
	}

	return result, err
}

func queryContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return qerror.New(qerror.CodeDeadlineExceeded, "query deadline exceeded")
	}

	return qerror.New(qerror.CodeCanceled, "query canceled")
}

// Start starts the processor chain and the dispatcher (acceptors are started
// separately since they call back into Handle). Components are started in
// dispatcher-to-front order so downstream is ready before upstream.
func (p *Pipeline) Start(ctx context.Context, host component.Host) error {
	err := processor.ValidateOrder(p.Processors)
	if err != nil {
		return fmt.Errorf("pipeline %q: %w", p.Name, err)
	}

	err = p.Dispatcher.Start(ctx, host)
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

func (p *Pipeline) handle(ctx context.Context, query *qdata.Query) (*qdata.Result, error) {
	if ctx.Err() != nil {
		return nil, queryContextError(ctx)
	}

	err := qdata.ValidateQueryTime(query)
	if err != nil {
		return nil, qerror.New(qerror.CodeInvalidArgument, "pipeline %q: %v", p.Name, err)
	}

	if identity, verified := qdata.IdentityFromContext(ctx); verified {
		qdata.SetTenantID(query, identity.Tenant)
	}

	err = p.processQuery(ctx, query)
	if err != nil {
		return nil, err
	}

	if p.AdmitQuota != nil && ctx.Value(quotaAdmittedKey{}) == nil {
		err := p.AdmitQuota(ctx)
		if err != nil {
			return nil, err
		}

		ctx = context.WithValue(ctx, quotaAdmittedKey{}, true)
	}

	return p.execute(ctx, query, p.Processors)
}

// execute builds the response chain around each dispatch processor's position.
func (p *Pipeline) execute(
	ctx context.Context, query *qdata.Query, processors []processor.Processor,
) (*qdata.Result, error) {
	if ctx.Err() != nil {
		return nil, queryContextError(ctx)
	}

	for index, proc := range processors {
		middleware, ok := proc.(processor.DispatchProcessor)
		if !ok {
			continue
		}

		next := func(ctx context.Context, query *qdata.Query) (*qdata.Result, error) {
			return p.execute(ctx, query, processors[index+1:])
		}
		respond := func(result *qdata.Result) (*qdata.Result, error) {
			return p.processResult(ctx, query, result, processors[:index])
		}

		result, err := middleware.ProcessDispatch(ctx, query, next, respond)
		if err != nil {
			return nil, fmt.Errorf("pipeline %q: %w", p.Name, err)
		}

		return result, nil
	}

	if identity, verified := qdata.IdentityFromContext(ctx); verified {
		qdata.SetTenantID(query, identity.Tenant)
	}

	result, err := p.Dispatcher.Dispatch(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("pipeline %q: %w", p.Name, err)
	}

	// Backfill once, before any response processor sees the dispatch result.
	if result.GetSignal() == qdata.SignalUnspecified && result.GetTable() == nil {
		result.Signal = query.GetSignal()
	}

	return p.processResult(ctx, query, result, processors)
}

func (p *Pipeline) processResult(
	ctx context.Context, query *qdata.Query, result *qdata.Result, processors []processor.Processor,
) (*qdata.Result, error) {
	for _, proc := range slices.Backward(processors) {
		err := proc.ProcessResult(ctx, query, result)
		if err != nil {
			return nil, fmt.Errorf("pipeline %q: %w", p.Name, err)
		}
	}

	return result, nil
}

// processQuery validates configuration and executes the request path, rejecting
// pending security constraints before the dispatcher can see the query.
func (p *Pipeline) processQuery(ctx context.Context, query *qdata.Query) error {
	err := processor.ValidateOrder(p.Processors)
	if err != nil {
		return fmt.Errorf("pipeline %q: %w", p.Name, err)
	}

	err = p.validatePlan(query)
	if err != nil {
		return err
	}

	// Mirror the plan's signal set onto Query.signals for downstream readers
	// (no-op without a plan).
	qdata.SyncPlanSignals(query)

	for _, proc := range p.Processors {
		if ctx.Err() != nil {
			return queryContextError(ctx)
		}

		err = proc.ProcessQuery(ctx, query)
		if err != nil {
			return fmt.Errorf("pipeline %q: %w", p.Name, err)
		}
	}

	if len(query.GetEnforcedMatchers()) > 0 || len(query.GetEnforcedPredicates()) > 0 {
		return qerror.New(qerror.CodeInternal, "pipeline %q: unapplied security constraints", p.Name)
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
