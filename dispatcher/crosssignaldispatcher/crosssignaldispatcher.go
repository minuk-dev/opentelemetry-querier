// Package crosssignaldispatcher executes a cross-signal query plan (issue #24):
// a plan whose Select nodes span several signals (e.g. metrics + logs). It routes
// each single-signal subtree to that signal's backend dispatcher and joins the
// per-signal results into a relational Table. Single-signal plans pass straight
// through to the one backend, so it also works as a signal-aware router.
package crosssignaldispatcher

import (
	"context"
	"fmt"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	qdatav1 "github.com/minuk-dev/opentelemetry-querier/gen/qdata/v1"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// Dispatcher fans a plan out to per-signal backends and joins their results.
type Dispatcher struct {
	dispatcher.Base

	// backends maps a signal to the dispatcher that serves it.
	backends map[qdata.Signal]dispatcher.Dispatcher
}

// New builds a cross-signal dispatcher over the given per-signal backends.
func New(backends map[qdata.Signal]dispatcher.Dispatcher) *Dispatcher {
	return &Dispatcher{Base: dispatcher.Base{}, backends: backends}
}

// Dispatch routes query to its backends. A single-signal plan is delegated
// whole to that signal's backend; a multi-signal plan must be a top-level
// BinaryOp join, whose two subtrees are executed separately and joined into a
// Table. Anything else fails closed.
func (d *Dispatcher) Dispatch(ctx context.Context, query *qdata.Query) (*qdata.Result, error) {
	plan := query.GetPlan()
	if plan == nil {
		return nil, qerror.New(qerror.CodeInvalidArgument, "crosssignaldispatcher: query has no plan")
	}

	signals := qdata.PlanSignals(plan)

	switch len(signals) {
	case 0:
		return nil, qerror.New(qerror.CodeInvalidArgument, "crosssignaldispatcher: plan targets no signal")
	case 1:
		return d.dispatchSingle(ctx, query, signals[0])
	default:
		return d.dispatchJoin(ctx, query, plan)
	}
}

// dispatchSingle delegates the whole query to the one backend that serves its
// signal, so the cross-signal dispatcher can sit in front of every query.
func (d *Dispatcher) dispatchSingle(
	ctx context.Context,
	query *qdata.Query,
	signal qdata.Signal,
) (*qdata.Result, error) {
	backend, ok := d.backends[signal]
	if !ok {
		return nil, qerror.New(qerror.CodeInvalidArgument,
			"crosssignaldispatcher: no backend for signal %s", signal)
	}

	result, err := backend.Dispatch(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("crosssignaldispatcher: dispatch %s: %w", signal, err)
	}

	return result, nil
}

// dispatchJoin handles a multi-signal plan. v1 supports exactly a top-level
// BinaryOp whose two operands are each single-signal subtrees; it executes each
// against its backend and joins the results on the BinaryOp's matching labels.
func (d *Dispatcher) dispatchJoin(
	ctx context.Context,
	query *qdata.Query,
	plan *qdata.QueryPlan,
) (*qdata.Result, error) {
	binary := plan.GetRoot().GetBinary()
	if binary == nil {
		return nil, qerror.New(qerror.CodeInvalidArgument,
			"crosssignaldispatcher: a cross-signal plan must be a top-level binary join")
	}

	left, err := d.execSide(ctx, query, binary.GetLhs())
	if err != nil {
		return nil, err
	}

	right, err := d.execSide(ctx, query, binary.GetRhs())
	if err != nil {
		return nil, err
	}

	table, err := joinTables(left.table, right.table, joinKeys(binary.GetMatching()))
	if err != nil {
		return nil, err
	}

	result := qdata.TableResult(table)
	result.Signal = qdata.SignalUnspecified
	mergeFeedback(result, left.result, right.result)

	return result, nil
}

// side is one executed operand of the join: its raw Result and normalized Table.
type side struct {
	result *qdata.Result
	table  *qdata.Table
}

// execSide runs one join operand against its signal's backend and normalizes the
// backend Result into a relational Table. The operand must be single-signal.
func (d *Dispatcher) execSide(ctx context.Context, parent *qdata.Query, subtree *qdata.Node) (*side, error) {
	signals := qdata.PlanSignals(qdata.Plan(subtree))
	if len(signals) != 1 {
		return nil, qerror.New(qerror.CodeInvalidArgument,
			"crosssignaldispatcher: a join operand must target exactly one signal, got %d", len(signals))
	}

	signal := signals[0]

	backend, ok := d.backends[signal]
	if !ok {
		return nil, qerror.New(qerror.CodeInvalidArgument,
			"crosssignaldispatcher: no backend for signal %s", signal)
	}

	result, err := backend.Dispatch(ctx, subQuery(parent, subtree, signal))
	if err != nil {
		return nil, fmt.Errorf("crosssignaldispatcher: dispatch %s operand: %w", signal, err)
	}

	table, err := resultToTable(signal, result)
	if err != nil {
		return nil, err
	}

	return &side{result: result, table: table}, nil
}

// subQuery wraps a join operand as its own single-signal query, carrying the
// parent's evaluation context (time range, step, tenant headers/metadata) so the
// backend evaluates both sides over the same window.
func subQuery(parent *qdata.Query, subtree *qdata.Node, signal qdata.Signal) *qdata.Query {
	return &qdata.Query{
		Signal:   signal,
		Context:  parent.GetContext(),
		Range:    parent.GetRange(),
		Step:     parent.GetStep(),
		Modifier: parent.GetModifier(),
		Header:   parent.GetHeader(),
		Metadata: parent.GetMetadata(),
		Plan:     qdata.Plan(subtree),
	}
}

// mergeFeedback copies the side results' notifications onto the joined result so
// upstream warnings survive the join.
func mergeFeedback(dst *qdata.Result, sides ...*qdata.Result) {
	for _, src := range sides {
		for _, note := range src.GetFeedback().GetNotifications() {
			qdata.Notify(dst, note.GetSeverity(), note.GetCode(), note.GetMessage(), note.GetSource())
		}
	}
}

// joinKeys returns the labels to equijoin on: the BinaryOp's `on` list. An empty
// list means "join on the tables' shared columns", resolved at join time.
func joinKeys(matching *qdatav1.VectorMatch) []string {
	return matching.GetOn()
}
