package pipeline_test

import (
	"testing"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestQueryCostBudget(t *testing.T) {
	t.Parallel()

	for _, scenario := range []struct {
		name      string
		budget    pipeline.CostBudget
		rejection string
	}{
		{"at boundary", pipeline.CostBudget{MaxRange: time.Minute, MaxPoints: 6, MaxPlanNodes: 3}, ""},
		{"dense range", pipeline.CostBudget{MaxRange: 0, MaxPoints: 5, MaxPlanNodes: 0}, "max_points"},
		{"joint plan complexity", pipeline.CostBudget{MaxRange: 0, MaxPoints: 0, MaxPlanNodes: 2}, "max_plan_nodes"},
		{
			"window too long",
			pipeline.CostBudget{MaxRange: time.Minute - time.Nanosecond, MaxPoints: 0, MaxPlanNodes: 0},
			"max_range",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			now := time.Now()
			query := &qdata.Query{
				Context: qdata.ContextRange,
				Range:   &qdata.TimeRange{Start: timestamppb.New(now), End: timestamppb.New(now.Add(time.Minute))},
				Step:    durationpb.New(30 * time.Second),
				Plan: qdata.Plan(qdata.BinaryNode(qdata.BinAnd,
					qdata.SelectNode(qdata.SignalMetrics, nil), qdata.SelectNode(qdata.SignalLogs, nil), nil)),
			}
			pipe := pipeline.New("test", nil, &stubDispatcher{Base: dispatcher.Base{}, result: &qdata.Result{}})
			pipe.Cost = scenario.budget

			result, err := pipe.Handle(t.Context(), query)
			if scenario.rejection == "" {
				require.NoError(t, err)
				require.NotNil(t, result)

				return
			}

			require.Equal(t, qerror.CodeResourceExhausted, qerror.CodeOf(err))
			require.ErrorContains(t, err, scenario.rejection)
		})
	}
}
