package prometheusacceptor_test

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/acceptor/prometheusacceptor"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher/prometheusdispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
)

// minStableQueries is the floor of real-world PromQL alert expressions (from the
// awesome-prometheus-alerts corpus) that must round-trip stably through the
// acceptor -> IR -> dispatcher path. It guards against coverage regressions; the
// remainder use PromQL features the IR does not yet model (changes/deriv/
// last_over_time/round, some group_right()+bool matching) and fail closed.
const minStableQueries = 1090

// loadCorpus reads the embedded PromQL corpus, skipping comments and blanks.
func loadCorpus(t *testing.T) []string {
	t.Helper()

	file, err := os.Open("testdata/awesome_prometheus_alerts_queries.txt")
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	var queries []string

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		queries = append(queries, line)
	}

	require.NoError(t, scanner.Err())

	return queries
}

// TestPromQLCorpusRoundTrip feeds a large corpus of real-world PromQL alert
// expressions through the Prometheus acceptor -> dispatcher path and asserts that
// the ones the IR supports render to a stable query (feeding the rendered query
// back produces the identical query — "the same query reaches the backend"), that
// no query ever causes a 5xx, and that overall coverage does not regress (#57).
func TestPromQLCorpusRoundTrip(t *testing.T) {
	t.Parallel()

	var captured string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		captured = r.FormValue("query")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer upstream.Close()

	pipe := pipeline.New("metrics", nil, prometheusdispatcher.New(
		prometheusdispatcher.Config{Endpoint: upstream.URL, TenantHeader: "", Timeout: 0}))
	acc := prometheusacceptor.New(prometheusacceptor.Config{Endpoint: ""}, pipe)

	front := httptest.NewServer(acc.Handler())
	defer front.Close()

	send := func(promQL string) (int, string) {
		captured = ""

		resp, err := http.Get(front.URL + "/api/v1/query?query=" + url.QueryEscape(promQL)) //nolint:noctx // test
		require.NoError(t, err)

		defer func() { _ = resp.Body.Close() }()

		return resp.StatusCode, captured
	}

	queries := loadCorpus(t)
	require.GreaterOrEqual(t, len(queries), minStableQueries, "corpus loaded")

	stable, unsupported := 0, 0

	for _, query := range queries {
		code, rendered := send(query)
		require.Less(t, code, http.StatusInternalServerError, "no query may cause a 5xx: %s", query)

		if code != http.StatusOK {
			unsupported++

			continue
		}

		// Supported: feeding the rendered query back must produce the identical
		// query, i.e. the query that reaches the backend is stable.
		code2, rendered2 := send(rendered)
		if code2 == http.StatusOK && rendered == rendered2 {
			stable++
		} else {
			unsupported++
		}
	}

	t.Logf("PromQL corpus: %d total, %d round-trip stable, %d unsupported", len(queries), stable, unsupported)
	assert.GreaterOrEqual(t, stable, minStableQueries, "real-world PromQL round-trip coverage must not regress")
}
