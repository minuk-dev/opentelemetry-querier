//go:build e2e

package e2e_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/minuk-dev/opentelemetry-querier/acceptor/prometheusacceptor"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher/prometheusdispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
)

// promConfig scrapes Prometheus itself every second, so the `up` metric has data
// within a couple of seconds of startup.
const promConfig = `global:
  scrape_interval: 1s
scrape_configs:
  - job_name: prometheus
    static_configs:
      - targets: ['localhost:9090']
`

// httpGetString GETs url and returns the status code and body.
func httpGetString(t *testing.T, url string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, string(body)
}

func TestPrometheusRealQuery(t *testing.T) { //nolint:paralleltest // containers run sequentially to bound memory
	ctx := context.Background()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "prom/prometheus:v2.54.1",
			ExposedPorts: []string{"9090/tcp"},
			Files: []testcontainers.ContainerFile{{
				Reader:            strings.NewReader(promConfig),
				ContainerFilePath: "/etc/prometheus/prometheus.yml",
				FileMode:          0o644,
			}},
			WaitingFor: wait.ForHTTP("/-/ready").WithPort("9090/tcp").WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err)

	defer func() { _ = container.Terminate(ctx) }()

	endpoint := containerURL(ctx, t, container, "9090/tcp")

	pipe := pipeline.New("metrics", nil, prometheusdispatcher.New(
		prometheusdispatcher.Config{Endpoint: endpoint, TenantHeader: "", Timeout: 0}))
	acc := prometheusacceptor.New(prometheusacceptor.Config{Endpoint: ""}, pipe)

	front := httptest.NewServer(acc.Handler())
	defer front.Close()

	// Self-scrape needs a moment; poll until the `up` series appears.
	var lastBody string

	require.Eventually(t, func() bool {
		code, body := httpGetString(t, front.URL+"/api/v1/query?query=up")
		lastBody = body

		return code == http.StatusOK && strings.Contains(body, `"__name__":"up"`)
	}, 30*time.Second, 1*time.Second, "up series never appeared; last body: %s", lastBody)

	assert.Contains(t, lastBody, `"job":"prometheus"`, "the self-scrape target is present")
	assert.Contains(t, lastBody, `"success"`)
}

// containerURL returns the http://host:mappedport base URL for a container port.
func containerURL(ctx context.Context, t *testing.T, container testcontainers.Container, port string) string {
	t.Helper()

	host, err := container.Host(ctx)
	require.NoError(t, err)

	mapped, err := container.MappedPort(ctx, port)
	require.NoError(t, err)

	return "http://" + net.JoinHostPort(host, mapped.Port())
}
