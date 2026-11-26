package pipeline_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/stretchr/testify/require"
)

func TestAuthorizationAudit(t *testing.T) {
	t.Parallel()

	for _, scenario := range []struct {
		name          string
		authenticated bool
		decision      string
	}{
		{"allowed", true, "allow"},
		{"credentials denied", false, "deny"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer

			var backend identityBackend

			pipe := pipeline.New("test", nil, &backend)
			pipe.AuditLogger = slog.New(slog.NewJSONHandler(&output, nil))
			pipe.Authenticate = func(context.Context, *qdata.Query) (pipeline.Principal, error) {
				if !scenario.authenticated {
					return pipeline.Principal{}, qerror.New(qerror.CodeUnauthenticated, "credential secret-token")
				}

				return pipeline.Principal{
					Identity: qdata.Identity{Authority: "idp", Principal: "alice", Tenant: "", Groups: nil, Roles: nil, Scopes: nil},
					Tenants:  []string{"a"},
				}, nil
			}
			query := &qdata.Query{Header: map[string]*qdata.HeaderValues{"Authorization": {Values: []string{"secret-token"}}}}
			qdata.SetMetadata(query, "client-principal", "spoofed-user")

			_, err := pipe.Handle(t.Context(), query)
			if scenario.authenticated {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}

			require.NotContains(t, output.String(), "secret-token")
			require.NotContains(t, output.String(), "spoofed-user")

			var event map[string]any
			require.NoError(t, json.Unmarshal(output.Bytes(), &event))
			require.Equal(t, scenario.decision, event["decision"])
			require.NotEmpty(t, event["request_id"])

			if scenario.authenticated {
				require.Equal(t, "alice", event["principal"])
				require.Equal(t, "a", event["tenant"])
			} else {
				require.Empty(t, event["principal"])
				require.Empty(t, backend.seen.Principal)
			}
		})
	}
}
