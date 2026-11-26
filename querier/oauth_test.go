package querier //nolint:testpackage // Exercises the internal introspection client with a TLS identity provider.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/stretchr/testify/require"
)

func TestIntrospection(t *testing.T) {
	t.Parallel()

	for _, scenario := range []struct {
		name, response string
		status         int
		code           qerror.Code
	}{
		{"active",
			`{"active":true,"iss":"idp","aud":"querier","sub":"alice","tenant":"team",` +
				`"scope":"query read","exp":%d}`, http.StatusOK, qerror.CodeInternal},
		{"inactive", `{"active":false,"exp":%d}`, http.StatusOK, qerror.CodeUnauthenticated},
		{"endpoint error", "", http.StatusServiceUnavailable, qerror.CodeUnavailable},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			stub := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				request.Body = http.MaxBytesReader(writer, request.Body, 1024)

				if request.Method != http.MethodPost || request.FormValue("token") != "opaque" {
					t.Error("unexpected introspection request")
				}

				writer.WriteHeader(scenario.status)

				if scenario.response != "" {
					_, _ = fmt.Fprintf(writer, scenario.response, time.Now().Add(time.Minute).Unix())
				}
			}))
			defer stub.Close()

			principal, err := introspect(t.Context(), stub.Client(), IntrospectionConfig{
				Endpoint: stub.URL, Issuer: "idp", Audience: "querier", TenantClaim: "tenant", ClientID: "querier",
				ClientSecretFile: "", Timeout: 0,
			}, "secret", "opaque")
			if scenario.code == qerror.CodeInternal {
				require.NoError(t, err)
				require.Equal(t, []string{"query", "read"}, principal.Scopes)
			} else {
				require.Equal(t, scenario.code, qerror.CodeOf(err))
			}
		})
	}
}
