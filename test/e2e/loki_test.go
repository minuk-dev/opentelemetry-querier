//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/minuk-dev/opentelemetry-querier/acceptor/lokiacceptor"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher/lokidispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
)

// sendJSON sends a JSON request and returns the status code and body.
func sendJSON(t *testing.T, method, url, body string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, string(raw)
}

func TestLokiRealQuery(t *testing.T) { //nolint:paralleltest // containers run sequentially to bound memory
	ctx := context.Background()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "grafana/loki:3.1.1",
			ExposedPorts: []string{"3100/tcp"},
			WaitingFor: wait.ForHTTP("/ready").WithPort("3100/tcp").
				WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }).
				WithStartupTimeout(120 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err)

	defer func() { _ = container.Terminate(ctx) }()

	endpoint := containerURL(ctx, t, container, "3100/tcp")

	// Push a log line into the job="e2e" stream.
	nowNanos := strconv.FormatInt(time.Now().UnixNano(), 10)
	push := fmt.Sprintf(`{"streams":[{"stream":{"job":"e2e"},"values":[[%q,"hello e2e"]]}]}`, nowNanos)

	require.Eventually(t, func() bool {
		code, _ := sendJSON(t, http.MethodPost, endpoint+"/loki/api/v1/push", push)

		return code == http.StatusNoContent
	}, 30*time.Second, 1*time.Second, "loki never accepted the push")

	pipe := pipeline.New("logs", nil, lokidispatcher.New(
		lokidispatcher.Config{Endpoint: endpoint, TenantHeader: "", Timeout: 0, Limit: 0, Direction: ""}))
	acc := lokiacceptor.New(lokiacceptor.Config{Endpoint: ""}, pipe)

	front := httptest.NewServer(acc.Handler())
	defer front.Close()

	start := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)
	end := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	query := front.URL + "/loki/api/v1/query_range?query=" + url.QueryEscape(`{job="e2e"}`) +
		"&start=" + start + "&end=" + end + "&step=60"

	var lastBody string

	require.Eventually(t, func() bool {
		code, body := httpGetString(t, query)
		lastBody = body

		return code == http.StatusOK && strings.Contains(body, "hello e2e")
	}, 30*time.Second, 1*time.Second, "pushed log never came back; last body: %s", lastBody)

	assert.Contains(t, lastBody, `"job":"e2e"`, "the stream label round-trips")
}
