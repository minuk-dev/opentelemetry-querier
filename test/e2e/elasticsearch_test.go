//go:build e2e

package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/minuk-dev/opentelemetry-querier/acceptor/elasticsearchacceptor"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher/elasticsearchdispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
)

func TestElasticsearchRealQuery(t *testing.T) { //nolint:paralleltest // containers run sequentially to bound memory
	ctx := context.Background()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "docker.elastic.co/elasticsearch/elasticsearch:8.15.0",
			ExposedPorts: []string{"9200/tcp"},
			Env: map[string]string{
				"discovery.type":         "single-node",
				"xpack.security.enabled": "false",
				"ES_JAVA_OPTS":           "-Xms512m -Xmx512m",
			},
			WaitingFor: wait.ForHTTP("/_cluster/health").WithPort("9200/tcp").
				WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }).
				WithStartupTimeout(180 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err)

	defer func() { _ = container.Terminate(ctx) }()

	endpoint := containerURL(ctx, t, container, "9200/tcp")

	// Index one document, refreshed so it is immediately searchable.
	doc := fmt.Sprintf(`{"level":"error","message":"boom e2e","@timestamp":%q}`,
		time.Now().UTC().Format(time.RFC3339))
	code, body := sendJSON(t, http.MethodPut, endpoint+"/logs/_doc/1?refresh=true", doc)
	require.Contains(t, []int{http.StatusOK, http.StatusCreated}, code, "index doc: %s", body)

	pipe := pipeline.New("logs", nil, elasticsearchdispatcher.New(elasticsearchdispatcher.Config{
		Endpoint: endpoint, Index: "", TimeField: "", Size: 0, Timeout: 0, Username: "", Password: "",
	}))
	acc := elasticsearchacceptor.New(elasticsearchacceptor.Config{Endpoint: ""}, pipe)

	front := httptest.NewServer(acc.Handler())
	defer front.Close()

	search := `{"query":{"query_string":{"query":"level:error"}}}`

	var lastBody string

	require.Eventually(t, func() bool {
		searchCode, searchBody := sendJSON(t, http.MethodPost, front.URL+"/_search", search)
		lastBody = searchBody

		return searchCode == http.StatusOK && strings.Contains(searchBody, "boom e2e")
	}, 30*time.Second, 1*time.Second, "indexed doc never came back; last body: %s", lastBody)

	assert.Contains(t, lastBody, "boom e2e", "the indexed document round-trips to the client")
}
