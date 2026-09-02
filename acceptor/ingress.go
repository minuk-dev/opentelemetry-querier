package acceptor

import (
	"net/http"
	"strings"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// PrepareIngress admits a client-supplied query into the pipeline. It is the
// trust boundary: every acceptor must call it on a query before handing it to
// its next consumer, even one it built itself from a native query, so the rules
// below hold for every transport and cannot drift per acceptor.
//
// It first clears the Query fields the pipeline owns (see sanitize), then copies
// the real transport headers onto the query (see injectHeaders).
func PrepareIngress(query *qdata.Query, header http.Header) {
	sanitize(query)
	injectHeaders(query, header)
}

// sanitize clears the Query fields that only the pipeline may write (issue #65).
// They ride on the request message, so a client can populate them over the wire,
// but they are pipeline state rather than client input:
//
//   - metadata carries processor-to-processor hints, among them tenant.id — the
//     trust anchor the tenant, query-rewrite, authz, rate-limit and dispatcher
//     components all read. A client that pre-set it chose its own tenant.
//   - enforced_matchers / enforced_predicates carry the isolation the enforcing
//     processors decided on. They are only ever AND-ed in, so a client cannot
//     widen a query through them today, but they are equally not client input.
//
// Both are cleared rather than merged: a processor that wants a hint on the
// query puts it there itself.
func sanitize(query *qdata.Query) {
	if query == nil {
		return
	}

	query.Metadata = nil
	query.EnforcedMatchers = nil
	query.EnforcedPredicates = nil
}

// injectHeaders copies header onto the query, with each source header taking
// precedence over any value already on the query. It removes case-insensitive
// duplicates so a header the client also set in the request body cannot shadow
// (or be shadowed by) the injected transport value: downstream lookups match
// case-insensitively, so two entries differing only in case would make the
// winner depend on map iteration order.
func injectHeaders(query *qdata.Query, header http.Header) {
	if query == nil || len(header) == 0 {
		return
	}

	if query.Header == nil {
		query.Header = make(map[string]*qdata.HeaderValues, len(header))
	}

	for key, values := range header {
		for existing := range query.Header {
			if existing != key && strings.EqualFold(existing, key) {
				delete(query.Header, existing)
			}
		}

		query.Header[key] = &qdata.HeaderValues{Values: values}
	}
}
