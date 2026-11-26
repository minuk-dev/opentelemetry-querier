package pipeline

import (
	"errors"
	"math/big"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

var errNegativeCostBudget = errors.New("query budgets must be non-negative")

// CostBudget limits estimated work before dispatch. Zero disables a limit.
type CostBudget struct {
	MaxRange     time.Duration `mapstructure:"max_range"`
	MaxPoints    int64         `mapstructure:"max_points"`
	MaxPlanNodes int64         `mapstructure:"max_plan_nodes"`
}

// Validate rejects negative query budgets.
func (b CostBudget) Validate() error {
	if b.MaxRange < 0 || b.MaxPoints < 0 || b.MaxPlanNodes < 0 {
		return errNegativeCostBudget
	}

	return nil
}

func (b CostBudget) check(query *qdata.Query) error {
	var disabled CostBudget
	if b == disabled {
		return nil
	}

	nodes, selectors := planCost(query.GetPlan().GetRoot())
	if b.MaxPlanNodes > 0 && nodes > b.MaxPlanNodes {
		return exceededBudget("max_plan_nodes")
	}

	if b.MaxRange > 0 && query.GetContext() == qdata.ContextRange {
		window := query.GetRange()
		if window.GetStart().AsTime().Add(b.MaxRange).Before(window.GetEnd().AsTime()) {
			return exceededBudget("max_range")
		}
	}

	if b.MaxPoints == 0 {
		return nil
	}

	evaluations := evaluationPoints(query)

	evaluations.Mul(evaluations, big.NewInt(max(1, selectors)))

	if !evaluations.IsInt64() || evaluations.Int64() > b.MaxPoints {
		return exceededBudget("max_points")
	}

	return nil
}

func evaluationPoints(query *qdata.Query) *big.Int {
	step := query.GetStep().AsDuration()
	if query.GetContext() != qdata.ContextRange || step <= 0 {
		return big.NewInt(1)
	}

	window := query.GetRange()
	start, end := window.GetStart().AsTime(), window.GetEnd().AsTime()

	// Protobuf timestamps can describe windows wider than time.Duration.
	points := big.NewInt(end.Unix() - start.Unix())
	points.Mul(points, big.NewInt(int64(time.Second)))
	points.Add(points, big.NewInt(int64(end.Nanosecond()-start.Nanosecond())))
	points.Quo(points, big.NewInt(int64(step)))

	return points.Add(points, big.NewInt(1))
}

func exceededBudget(limit string) error {
	return qerror.New(qerror.CodeResourceExhausted, "query cost exceeds %s", limit)
}

func planCost(node *qdata.Node) (int64, int64) {
	if node == nil {
		return 0, 0
	}

	nodes := int64(1)

	var selectors int64

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
