package queryrewriteprocessor_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/processor/queryrewriteprocessor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

const testTenant = "acme"

// tenantConfig enforces tenant_id from the resolved tenant.
func tenantConfig() queryrewriteprocessor.Config {
	return queryrewriteprocessor.Config{
		EnforceLabels: []queryrewriteprocessor.EnforceLabel{{Name: "tenant_id", Value: "", FromTenant: true}},
	}
}

func leafPred(name, value string) *qdata.Predicate {
	return qdata.LeafPredicate(&qdata.LabelMatcher{Name: name, Op: qdata.MatchEqual, Value: value})
}

// metricSelectQuery builds a query whose plan is a single metrics Select over the
// given filter.
func metricSelectQuery(filter *qdata.Predicate) *qdata.Query {
	return &qdata.Query{Plan: qdata.Plan(qdata.SelectNode(qdata.SignalMetrics, filter))}
}

// selectFilter returns the filter of the plan root's Select node.
func selectFilter(t *testing.T, query *qdata.Query) *qdata.Predicate {
	t.Helper()

	sel := query.GetPlan().GetRoot().GetSelect()
	require.NotNil(t, sel, "plan root should be a Select")

	return sel.GetFilter()
}

// matcherMap flattens a filter into a name->value map (pure conjunctions only).
func matcherMap(t *testing.T, filter *qdata.Predicate) map[string]string {
	t.Helper()

	matchers, ok := qdata.FlattenConjunction([]*qdata.Predicate{filter})
	require.True(t, ok, "filter should flatten to a conjunction")

	out := map[string]string{}
	for _, matcher := range matchers {
		out[matcher.GetName()] = matcher.GetValue()
	}

	return out
}

func TestInjectsTenantMatcher(t *testing.T) {
	t.Parallel()

	query := metricSelectQuery(leafPred("__name__", "up"))
	qdata.SetTenantID(query, testTenant)

	require.NoError(t, queryrewriteprocessor.New(tenantConfig()).ProcessQuery(context.Background(), query))

	labels := matcherMap(t, selectFilter(t, query))
	assert.Equal(t, "up", labels["__name__"])
	assert.Equal(t, testTenant, labels["tenant_id"])
}

func TestInjectsIntoEverySelect(t *testing.T) {
	t.Parallel()

	lhs := qdata.SelectNode(qdata.SignalMetrics, leafPred("__name__", "a"))
	rhs := qdata.SelectNode(qdata.SignalMetrics, leafPred("__name__", "b"))
	query := &qdata.Query{Plan: qdata.Plan(qdata.BinaryNode(qdata.BinDiv, lhs, rhs, nil))}
	qdata.SetTenantID(query, testTenant)

	require.NoError(t, queryrewriteprocessor.New(tenantConfig()).ProcessQuery(context.Background(), query))

	binary := query.GetPlan().GetRoot().GetBinary()
	require.NotNil(t, binary)
	assert.Equal(t, testTenant, matcherMap(t, binary.GetLhs().GetSelect().GetFilter())["tenant_id"])
	assert.Equal(t, testTenant, matcherMap(t, binary.GetRhs().GetSelect().GetFilter())["tenant_id"])
}

func TestEnforcedMatchersFromQuery(t *testing.T) {
	t.Parallel()

	query := metricSelectQuery(leafPred("__name__", "up"))
	query.EnforcedMatchers = []*qdata.LabelMatcher{{Name: "namespace", Op: qdata.MatchEqual, Value: "prod"}}

	proc := queryrewriteprocessor.New(queryrewriteprocessor.Config{EnforceLabels: nil})
	require.NoError(t, proc.ProcessQuery(context.Background(), query))

	assert.Equal(t, "prod", matcherMap(t, selectFilter(t, query))["namespace"])
}

func TestEnforcedPredicatesConjunctionComposed(t *testing.T) {
	t.Parallel()

	query := metricSelectQuery(leafPred("__name__", "up"))
	query.EnforcedPredicates = []*qdata.Predicate{
		qdata.BoolPredicate(qdata.BoolAnd, leafPred("namespace", "prod"), leafPred("region", "eu")),
	}

	proc := queryrewriteprocessor.New(queryrewriteprocessor.Config{EnforceLabels: nil})
	require.NoError(t, proc.ProcessQuery(context.Background(), query))

	labels := matcherMap(t, selectFilter(t, query))
	assert.Equal(t, "prod", labels["namespace"])
	assert.Equal(t, "eu", labels["region"])
}

func TestBooleanEnforcementComposesInsteadOfFailingClosed(t *testing.T) {
	t.Parallel()

	// An OR enforcement predicate can now be composed into the plan's predicate
	// tree (which supports OR/NOT), instead of failing closed as the old
	// PromQL-selector injector did.
	query := metricSelectQuery(leafPred("__name__", "up"))
	query.EnforcedPredicates = []*qdata.Predicate{
		qdata.BoolPredicate(qdata.BoolOr, leafPred("env", "prod"), leafPred("env", "staging")),
	}

	proc := queryrewriteprocessor.New(queryrewriteprocessor.Config{EnforceLabels: nil})
	require.NoError(t, proc.ProcessQuery(context.Background(), query), "OR enforcement now composes, not fails closed")

	filter := selectFilter(t, query)
	require.NoError(t, qdata.ValidatePredicate(filter), "composed filter must be well-formed")

	_, flat := qdata.FlattenConjunction([]*qdata.Predicate{filter})
	assert.False(t, flat, "the OR survives in the tree, so the filter is not a plain conjunction")
}

func TestNoPlanIsNoop(t *testing.T) {
	t.Parallel()

	query := &qdata.Query{}
	qdata.SetTenantID(query, testTenant)

	require.NoError(t, queryrewriteprocessor.New(tenantConfig()).ProcessQuery(context.Background(), query))
	assert.Nil(t, query.GetPlan())
}

func TestNoEnforcementLeavesFilterUntouched(t *testing.T) {
	t.Parallel()

	query := metricSelectQuery(leafPred("__name__", "up"))

	proc := queryrewriteprocessor.New(queryrewriteprocessor.Config{EnforceLabels: nil})
	require.NoError(t, proc.ProcessQuery(context.Background(), query))

	labels := matcherMap(t, selectFilter(t, query))
	assert.Equal(t, map[string]string{"__name__": "up"}, labels)
}
