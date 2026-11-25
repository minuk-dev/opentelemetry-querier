package querier

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
	for _, tc := range []struct {
		name, response string
		status         int
		code           qerror.Code
	}{
		{"active", `{"active":true,"iss":"idp","aud":"querier","sub":"alice","tenant":"team","scope":"query read","exp":%d}`, 200, qerror.CodeInternal},
		{"inactive", `{"active":false,"exp":%d}`, 200, qerror.CodeUnauthenticated},
		{"endpoint error", "", 503, qerror.CodeUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.FormValue("token") != "opaque" {
					t.Error("unexpected introspection request")
				}
				w.WriteHeader(tc.status)
				if tc.response != "" {
					_, _ = fmt.Fprintf(w, tc.response, time.Now().Add(time.Minute).Unix())
				}
			}))
			defer stub.Close()
			principal, err := introspect(t.Context(), stub.Client(), IntrospectionConfig{
				Endpoint: stub.URL, Issuer: "idp", Audience: "querier", TenantClaim: "tenant", ClientID: "querier",
			}, "secret", "opaque")
			if tc.code == qerror.CodeInternal {
				require.NoError(t, err)
				require.Equal(t, []string{"query", "read"}, principal.Scopes)
			} else {
				require.Equal(t, tc.code, qerror.CodeOf(err))
			}
		})
	}
}
