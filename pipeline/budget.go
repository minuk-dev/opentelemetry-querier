package pipeline

import (
	"errors"
	"math/big"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// CostBudget limits estimated work before dispatch. Zero disables a limit.
type CostBudget struct {
	MaxRange     time.Duration `mapstructure:"max_range"`
	MaxPoints    int64         `mapstructure:"max_points"`
	MaxPlanNodes int64         `mapstructure:"max_plan_nodes"`
}

// Validate rejects negative query budgets.
func (b CostBudget) Validate() error {
	if b.MaxRange < 0 || b.MaxPoints < 0 || b.MaxPlanNodes < 0 {
		return errors.New("query budgets must be non-negative")
	}
	return nil
}

func (b CostBudget) check(query *qdata.Query) error {
	if b == (CostBudget{}) {
		return nil
	}
	nodes, selectors := planCost(query.GetPlan().GetRoot())
	if b.MaxPlanNodes > 0 && nodes > b.MaxPlanNodes {
		return exceededBudget("max_plan_nodes")
	}

	evaluations := big.NewInt(1)
	if query.GetContext() == qdata.ContextRange {
		window := query.GetRange()
		start, end := window.GetStart().AsTime(), window.GetEnd().AsTime()
		if b.MaxRange > 0 && start.Add(b.MaxRange).Before(end) {
			return exceededBudget("max_range")
		}
		if step := query.GetStep().AsDuration(); step > 0 {
			// Protobuf timestamps can describe windows wider than time.Duration.
			evaluations.SetInt64(end.Unix() - start.Unix())
			evaluations.Mul(evaluations, big.NewInt(int64(time.Second)))
			evaluations.Add(evaluations, big.NewInt(int64(end.Nanosecond()-start.Nanosecond())))
			evaluations.Quo(evaluations, big.NewInt(int64(step)))
			evaluations.Add(evaluations, big.NewInt(1))
		}
	}
	evaluations.Mul(evaluations, big.NewInt(max(1, selectors)))
	if b.MaxPoints > 0 && (!evaluations.IsInt64() || evaluations.Int64() > b.MaxPoints) {
		return exceededBudget("max_points")
	}
	return nil
}

func exceededBudget(limit string) error {
	return qerror.New(qerror.CodeResourceExhausted, "query cost exceeds %s", limit)
}

func planCost(node *qdata.Node) (nodes, selectors int64) {
	if node == nil {
		return 0, 0
	}
	nodes = 1
	visit := func(child *qdata.Node) {
		childNodes, childSelectors := planCost(child)
		nodes += childNodes
		selectors += childSelectors
	}
	switch {
	case node.GetSelect() != nil:
		selectors++
	case node.GetTimeAgg() != nil:
		visit(node.GetTimeAgg().GetInput())
	case node.GetAggregate() != nil:
		visit(node.GetAggregate().GetInput())
	case node.GetBinary() != nil:
		visit(node.GetBinary().GetLhs())
		visit(node.GetBinary().GetRhs())
	case node.GetFunction() != nil:
		for _, arg := range node.GetFunction().GetArgs() {
			visit(arg)
		}
	}
	return nodes, selectors
}
