package pipeline_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/acceptor/prometheusacceptor"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher/prometheusdispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/processor"
)

// maxBodyBytes bounds the form body the upstream parses (gosec G120).
const maxBodyBytes = 1 << 20

// tenantHeader is the multi-tenancy header shared by the tenant processor and
// the dispatcher, mirroring config.yaml.
const tenantHeader = "X-Scope-OrgID"

// okVectorBody is a minimal successful Prometheus instant-query response with a
// single `up` sample. Tests that assert on the response path override it.
const okVectorBody = `{"status":"success","data":{"resultType":"vector",` +
	`"result":[{"metric":{"__name__":"up","job":"api"},"value":[1700000000,"1"]}]}}`

// fakeUpstream is a fake Prometheus that records what the dispatcher rendered and
// forwarded, and replies with a configurable body. It stands in for real
// storage so the tests stay hermetic (no Docker).
type fakeUpstream struct {
	server *httptest.Server

	mu     sync.Mutex
	query  string // the rendered PromQL `query` form value
	tenant string // the forwarded tenant header value
	body   string // the response body to return
}

// newUpstream starts a fake Prometheus returning okVectorBody until body is set.
func newUpstream(t *testing.T) *fakeUpstream {
	t.Helper()

	upstream := &fakeUpstream{server: nil, mu: sync.Mutex{}, query: "", tenant: "", body: okVectorBody}

	upstream.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Body = http.MaxBytesReader(writer, request.Body, maxBodyBytes)

		upstream.mu.Lock()
		upstream.query = request.FormValue("query")
		// Get canonicalizes at runtime; the const mirrors config.yaml verbatim.
		upstream.tenant = request.Header.Get(tenantHeader)
		body := upstream.body
		upstream.mu.Unlock()

		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(upstream.server.Close)

	return upstream
}

// setBody swaps the response body the upstream returns.
func (u *fakeUpstream) setBody(body string) {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.body = body
}

// gotQuery returns the last rendered PromQL query the upstream received.
func (u *fakeUpstream) gotQuery() string {
	u.mu.Lock()
	defer u.mu.Unlock()

	return u.query
}

// gotTenant returns the last tenant header value the upstream received.
func (u *fakeUpstream) gotTenant() string {
	u.mu.Lock()
	defer u.mu.Unlock()

	return u.tenant
}

// front wires a Prometheus acceptor -> the given processor chain -> a real
// Prometheus dispatcher pointed at up, and returns the client-facing base URL.
func front(t *testing.T, up *fakeUpstream, procs ...processor.Processor) string {
	t.Helper()

	disp := prometheusdispatcher.New(
		prometheusdispatcher.Config{Endpoint: up.server.URL, TenantHeader: tenantHeader, Timeout: 0})

	return frontWith(t, disp, procs...)
}

// frontWith is front with an explicit terminating dispatcher, letting a test
// substitute a fake that returns a hand-built result on the response path.
func frontWith(t *testing.T, disp dispatcher.Dispatcher, procs ...processor.Processor) string {
	t.Helper()

	pipe := pipeline.New("metrics", procs, disp)
	acc := prometheusacceptor.New(prometheusacceptor.Config{Endpoint: ""}, pipe)

	server := httptest.NewServer(acc.Handler())
	t.Cleanup(server.Close)

	return server.URL
}

// getWith issues GET base/api/v1/query?query=up with the given request headers
// and returns the status code and body.
func getWith(t *testing.T, base string, headers map[string]string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/api/v1/query?query=up", nil)
	require.NoError(t, err)

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, string(body)
}
