// Package crosssignalconnector executes a cross-signal query plan (issue #24) by
// bridging pipelines: a plan whose Select nodes span several signals (e.g.
// metrics + logs) is split, and each single-signal subtree runs through that
// signal's own pipeline — processors and all — before the per-signal results are
// joined into a relational Table. A single-signal plan is delegated whole to that
// signal's pipeline, so the connector also works as a signal-aware router. Unlike
// a dispatcher, it targets whole pipelines (pipeline.Handler), so per-signal
// enforcement is applied to every sub-query rather than bypassed.
package crosssignalconnector

import (
	"context"
	"fmt"

	"github.com/minuk-dev/opentelemetry-querier/connector"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// Connector fans a plan out to per-signal target pipelines and joins their
// results.
type Connector struct {
	dispatcher.Base

	// targets maps a signal to the pipeline that serves it.
	targets map[qdata.Signal]pipeline.Handler
}

// compile-time assertion that Connector satisfies the category interface.
var _ connector.Connector = (*Connector)(nil)

// New builds a cross-signal connector over the given per-signal target pipelines.
func New(targets map[qdata.Signal]pipeline.Handler) *Connector {
	return &Connector{Base: dispatcher.Base{}, targets: targets}
}

// Dispatch routes query to its target pipelines. A single-signal plan is
// delegated whole to that signal's pipeline; a multi-signal plan must be a
// top-level BinaryOp join, whose two subtrees are executed separately (each
// through its pipeline) and joined into a Table. Anything else fails closed.
func (c *Connector) Dispatch(ctx context.Context, query *qdata.Query) (*qdata.Result, error) {
	plan := query.GetPlan()
	if plan == nil {
		return nil, qerror.New(qerror.CodeInvalidArgument, "crosssignalconnector: query has no plan")
	}

	signals := qdata.PlanSignals(plan)

	switch len(signals) {
	case 0:
		return nil, qerror.New(qerror.CodeInvalidArgument, "crosssignalconnector: plan targets no signal")
	case 1:
		return c.dispatchSingle(ctx, query, signals[0])
	default:
		return c.dispatchJoin(ctx, query, plan)
	}
}

// dispatchSingle delegates the whole query to the one pipeline that serves its
// signal, so the connector can sit in front of every query.
func (c *Connector) dispatchSingle(
	ctx context.Context,
	query *qdata.Query,
	signal qdata.Signal,
) (*qdata.Result, error) {
	target, ok := c.targets[signal]
	if !ok {
		return nil, qerror.New(qerror.CodeInvalidArgument,
			"crosssignalconnector: no target pipeline for signal %s", signal)
	}

	result, err := target.Handle(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("crosssignalconnector: handle %s: %w", signal, err)
	}

	return result, nil
}

// dispatchJoin handles a multi-signal plan. v1 supports exactly a top-level
// BinaryOp whose two operands are each single-signal subtrees; it executes each
// through its pipeline and joins the results on the BinaryOp's matching labels.
func (c *Connector) dispatchJoin(
	ctx context.Context,
	query *qdata.Query,
	plan *qdata.QueryPlan,
) (*qdata.Result, error) {
	binary := plan.GetRoot().GetBinary()
	if binary == nil {
		return nil, qerror.New(qerror.CodeInvalidArgument,
			"crosssignalconnector: a cross-signal plan must be a top-level binary join")
	}

	mode, err := joinModeFor(binary.GetOp())
	if err != nil {
		return nil, err
	}

	left, err := c.execSide(ctx, query, binary.GetLhs())
	if err != nil {
		return nil, err
	}

	right, err := c.execSide(ctx, query, binary.GetRhs())
	if err != nil {
		return nil, err
	}

	table, err := joinTables(left.table, right.table, binary.GetMatching(), mode)
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

// execSide runs one join operand through its signal's pipeline and normalizes the
// Result into a relational Table. The operand must be single-signal.
func (c *Connector) execSide(ctx context.Context, parent *qdata.Query, subtree *qdata.Node) (*side, error) {
	signals := qdata.PlanSignals(qdata.Plan(subtree))
	if len(signals) != 1 {
		return nil, qerror.New(qerror.CodeInvalidArgument,
			"crosssignalconnector: a join operand must target exactly one signal, got %d", len(signals))
	}

	signal := signals[0]

	target, ok := c.targets[signal]
	if !ok {
		return nil, qerror.New(qerror.CodeInvalidArgument,
			"crosssignalconnector: no target pipeline for signal %s", signal)
	}

	result, err := target.Handle(ctx, subQuery(parent, subtree, signal))
	if err != nil {
		return nil, fmt.Errorf("crosssignalconnector: handle %s operand: %w", signal, err)
	}

	table, err := resultToTable(signal, result)
	if err != nil {
		return nil, err
	}

	return &side{result: result, table: table}, nil
}

// subQuery wraps a join operand as its own single-signal query, carrying the
// parent's evaluation context (time range, step, tenant headers/metadata) so both
// sides are evaluated over the same window.
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
