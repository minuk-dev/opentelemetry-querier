package querier_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/querier"
)

// writeConfig writes body to a temp file and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	return path
}

// pipelineConfig renders a minimal, otherwise-valid config with a single
// pipeline named id.
func pipelineConfig(id string) string {
	return fmt.Sprintf(
		"service:\n  pipelines:\n    %s:\n      acceptors: [otqp]\n      dispatchers: [prometheus]\n", id)
}

func TestLoadConfigPipelineSignalTyping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{name: "metrics", id: "metrics", wantErr: false},
		{name: "logs with instance name", id: "logs/cross", wantErr: false},
		{name: "traces token maps to spans", id: "traces", wantErr: false},
		{name: "profiles", id: "profiles", wantErr: false},
		{name: "non-signal type rejected", id: "query/default", wantErr: true},
		{name: "arbitrary type rejected", id: "foo", wantErr: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := querier.LoadConfig(writeConfig(t, pipelineConfig(testCase.id)))
			if testCase.wantErr {
				require.ErrorContains(t, err, "signal")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
