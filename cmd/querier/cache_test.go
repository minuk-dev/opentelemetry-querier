package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/querier"
)

func TestBuildWithCacheProcessor(t *testing.T) {
	t.Parallel()

	const config = `
acceptors:
  otqp: {}
processors:
  cache/views:
    ttl: 30s
    max_bytes: 16384
    key: '{{ with index .Header "X-View" }}{{ index .Values 0 }}{{ end }}'
  responsefilter: {}
dispatchers:
  prometheus: {}
service:
  pipelines:
    metrics/cache:
      acceptors: [otqp]
      processors: [cache/views, responsefilter]
      dispatchers: [prometheus]
`

	path := filepath.Join(t.TempDir(), "config.yaml")

	err := os.WriteFile(path, []byte(config), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := querier.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	factories, err := components()
	if err != nil {
		t.Fatal(err)
	}

	_, err = querier.Build(factories, cfg, component.BuildInfo{Command: "test", Version: ""})
	if err != nil {
		t.Fatal(err)
	}
}
