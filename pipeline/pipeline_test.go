package pipeline_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// recordingProc records the order in which its hooks run.
type recordingProc struct {
	processor.Base

	name  string
	trace *[]string
}

func (p *recordingProc) ProcessQuery(_ context.Context, _ *qdata.Query) error {
	*p.trace = append(*p.trace, "query:"+p.name)

	return nil
}

func (p *recordingProc) ProcessResult(_ context.Context, _ *qdata.Query, _ *qdata.Result) error {
	*p.trace = append(*p.trace, "result:"+p.name)

	return nil
}

type stubDispatcher struct {
	dispatcher.Base

	result *qdata.Result
}

func (d *stubDispatcher) Dispatch(_ context.Context, _ *qdata.Query) (*qdata.Result, error) {
	return d.result, nil
}

func TestHandleRunsRequestForwardAndResultReverse(t *testing.T) {
	t.Parallel()

	var trace []string

	procA := &recordingProc{Base: processor.Base{}, name: "a", trace: &trace}
	procB := &recordingProc{Base: processor.Base{}, name: "b", trace: &trace}
	pipe := pipeline.New("test",
		[]processor.Processor{procA, procB},
		&stubDispatcher{Base: dispatcher.Base{}, result: &qdata.Result{}},
	)

	_, err := pipe.Handle(context.Background(), &qdata.Query{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	want := []string{"query:a", "query:b", "result:b", "result:a"}
	if len(trace) != len(want) {
		t.Fatalf("trace = %v, want %v", trace, want)
	}

	for i := range want {
		if trace[i] != want[i] {
			t.Fatalf("trace = %v, want %v", trace, want)
		}
	}
}

func TestHandleDefaultsResultSignalFromQuery(t *testing.T) {
	t.Parallel()

	pipe := pipeline.New("test", nil, &stubDispatcher{Base: dispatcher.Base{}, result: &qdata.Result{}})

	result, err := pipe.Handle(context.Background(), &qdata.Query{Signal: qdata.SignalMetrics})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if result.GetSignal() != qdata.SignalMetrics {
		t.Fatalf("signal = %v, want metrics", result.GetSignal())
	}
}

func TestHandlePopulatesQuerySignalsFromPlan(t *testing.T) {
	t.Parallel()

	// A cross-signal plan (metrics + logs); Handle mirrors its signal set onto
	// Query.signals before any processor runs.
	plan := qdata.Plan(qdata.BinaryNode(qdata.BinAnd,
		qdata.SelectNode(qdata.SignalMetrics, nil),
		qdata.SelectNode(qdata.SignalLogs, nil), nil))
	query := &qdata.Query{Plan: plan}

	pipe := pipeline.New("test", nil, &stubDispatcher{Base: dispatcher.Base{}, result: &qdata.Result{}})

	_, err := pipe.Handle(context.Background(), query)
	require.NoError(t, err)
	assert.Equal(t, []qdata.Signal{qdata.SignalMetrics, qdata.SignalLogs}, query.GetSignals())
}

func TestHandleLeavesSignalsEmptyWithoutPlan(t *testing.T) {
	t.Parallel()

	query := &qdata.Query{Signal: qdata.SignalMetrics}

	pipe := pipeline.New("test", nil, &stubDispatcher{Base: dispatcher.Base{}, result: &qdata.Result{}})

	_, err := pipe.Handle(context.Background(), query)
	require.NoError(t, err)
	assert.Empty(t, query.GetSignals(), "no plan means no signal set to mirror")
}

// TestHandleRejectsMalformedPlan is the regression test for issue #67: the
// pipeline is the one place every acceptor funnels through, so it runs
// qdata.ValidatePlan on the client's plan and rejects a malformed one as
// CodeInvalidArgument before any processor or dispatcher sees it. Without it a
// zero time_agg window surfaced as an upstream failure, an aggregate setting
// both by and without silently lost its without, and an empty function name
// rendered as "(...)".
func TestHandleRejectsMalformedPlan(t *testing.T) {
	t.Parallel()

	metrics := qdata.SelectNode(qdata.SignalMetrics, nil)

	cases := []struct {
		name string
		plan *qdata.QueryPlan
	}{
		{
			name: "time_agg zero window",
			plan: qdata.Plan(qdata.TimeAggNode(qdata.TimeAggRate, 0, metrics)),
		},
		{
			name: "aggregate by and without",
			plan: qdata.Plan(qdata.AggregateNode(qdata.AggSum, []string{"job"}, []string{"instance"}, 0, metrics)),
		},
		{
			name: "function empty name",
			plan: qdata.Plan(qdata.FunctionNode("", []*qdata.Node{metrics})),
		},
		{
			name: "empty node",
			plan: qdata.Plan(&qdata.Node{}),
		},
		{
			name: "nil root",
			plan: qdata.Plan(nil),
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			var trace []string

			proc := &recordingProc{Base: processor.Base{}, name: "a", trace: &trace}
			pipe := pipeline.New("test",
				[]processor.Processor{proc},
				&stubDispatcher{Base: dispatcher.Base{}, result: &qdata.Result{}},
			)

			_, err := pipe.Handle(context.Background(), &qdata.Query{Plan: testCase.plan})
			require.Error(t, err)
			assert.Equal(t, qerror.CodeInvalidArgument, qerror.CodeOf(err))
			assert.Empty(t, trace, "a malformed plan must not reach the processors or the dispatcher")
		})
	}
}

// TestHandleAcceptsWellFormedPlan pins the other side of the check: a plan that
// ValidatePlan accepts still runs the full chain.
func TestHandleAcceptsWellFormedPlan(t *testing.T) {
	t.Parallel()

	var trace []string

	proc := &recordingProc{Base: processor.Base{}, name: "a", trace: &trace}
	plan := qdata.Plan(qdata.AggregateNode(qdata.AggSum, []string{"job"}, nil, 0,
		qdata.TimeAggNode(qdata.TimeAggRate, time.Minute,
			qdata.SelectNode(qdata.SignalMetrics, nil))))
	pipe := pipeline.New("test",
		[]processor.Processor{proc},
		&stubDispatcher{Base: dispatcher.Base{}, result: &qdata.Result{}},
	)

	_, err := pipe.Handle(context.Background(), &qdata.Query{Plan: plan})
	require.NoError(t, err)
	assert.Equal(t, []string{"query:a", "result:a"}, trace)
}
