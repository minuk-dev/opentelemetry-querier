package acceptor_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/acceptor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// The tenant a client would spoof, the tenant an upstream gateway actually
// authenticated, and the canonical spelling of the tenancy header carrying it.
const (
	spoofedTenant = "victim-corp"
	realTenant    = "acme"
	orgIDHeader   = "X-Scope-Orgid"
)

// TestPrepareIngressStripsPipelineState is the regression test for issue #65:
// metadata, enforced matchers and enforced predicates are pipeline state, but
// they ride on the request message, so a client can send them. The trust
// boundary must drop them — otherwise a client picks its own tenant.id, the
// trust anchor every tenancy-aware component reads.
func TestPrepareIngressStripsPipelineState(t *testing.T) {
	t.Parallel()

	query := &qdata.Query{
		Metadata: map[string]string{qdata.MetadataTenantID: spoofedTenant, "queryrewrite.rewritten": "true"},
		EnforcedMatchers: []*qdata.LabelMatcher{
			{Name: "tenant", Op: qdata.MatchEqual, Value: spoofedTenant},
		},
		EnforcedPredicates: []*qdata.Predicate{
			qdata.LeafPredicate(&qdata.LabelMatcher{Name: "tenant", Op: qdata.MatchEqual, Value: spoofedTenant}),
		},
	}

	acceptor.PrepareIngress(query, http.Header{orgIDHeader: []string{realTenant}})

	assert.Empty(t, query.GetMetadata(), "client-supplied metadata must not reach the pipeline")
	assert.Empty(t, query.GetEnforcedMatchers(), "enforcement is the pipeline's to decide, not the client's")
	assert.Empty(t, query.GetEnforcedPredicates(), "enforcement is the pipeline's to decide, not the client's")
	assert.Empty(t, qdata.TenantID(query), "the tenant is resolved from the header, downstream of ingress")

	// Sanitizing must not cost the query its transport headers.
	require.Contains(t, query.GetHeader(), orgIDHeader)
	assert.Equal(t, []string{realTenant}, query.GetHeader()[orgIDHeader].GetValues())
}

// TestPrepareIngressSanitizesWithoutHeaders covers the transport that carries no
// headers at all (a gRPC call without metadata): the query must still be
// sanitized, since an early return on the header injection would leave the
// client's metadata in place.
func TestPrepareIngressSanitizesWithoutHeaders(t *testing.T) {
	t.Parallel()

	query := &qdata.Query{Metadata: map[string]string{qdata.MetadataTenantID: spoofedTenant}}

	acceptor.PrepareIngress(query, nil)

	assert.Empty(t, query.GetMetadata())
}

// TestPrepareIngressHeadersWin pins the header rules the OTQP acceptor already
// relied on and the native acceptors did not have: a transport header overrides
// a body-supplied one, and no case-variant duplicate is left behind for a
// case-insensitive lookup to pick between (its winner would otherwise depend on
// map iteration order).
func TestPrepareIngressHeadersWin(t *testing.T) {
	t.Parallel()

	query := &qdata.Query{Header: map[string]*qdata.HeaderValues{
		"X-Scope-OrgID": {Values: []string{"from-body"}},
		"X-Other":       {Values: []string{"kept"}},
	}}

	acceptor.PrepareIngress(query, http.Header{"x-scope-orgid": []string{realTenant}})

	matches := map[string][]string{}

	for key, values := range query.GetHeader() {
		if http.CanonicalHeaderKey(key) == orgIDHeader {
			matches[key] = values.GetValues()
		}
	}

	assert.Equal(t, map[string][]string{"x-scope-orgid": {realTenant}}, matches,
		"the transport value must be the only x-scope-orgid entry, got header map %v", query.GetHeader())
	assert.Equal(t, []string{"kept"}, query.GetHeader()["X-Other"].GetValues(),
		"an unrelated body header is left alone")
}

// TestPrepareIngressNilQuery guards the decode path where the request carried no
// query at all: the acceptor hands the nil through and must not panic.
func TestPrepareIngressNilQuery(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() {
		acceptor.PrepareIngress(nil, http.Header{orgIDHeader: []string{realTenant}})
	})
}
