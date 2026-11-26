package querier //nolint:testpackage // Exercises the internal credential verifier with a mounted hash.

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestBasicAuthenticator(t *testing.T) {
	t.Parallel()

	hash, err := bcrypt.GenerateFromPassword([]byte("valid-password"), 10)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "hash")
	require.NoError(t, os.WriteFile(path, hash, 0600))
	authenticate, err := basicAuthenticator(BasicIdentityConfig{
		Authority: "accounts", Realm: "Queries",
		Accounts: map[string]BasicAccount{"alice": {HashFile: path, Tenants: []string{"tenant"}}},
	})
	require.NoError(t, err)

	var request http.Request

	request.Header = make(http.Header)
	request.SetBasicAuth("alice", "valid-password")
	query := &qdata.Query{Header: map[string]*qdata.HeaderValues{
		"Authorization": {Values: request.Header.Values("Authorization")},
	}}
	principal, err := authenticate(t.Context(), query)
	require.NoError(t, err)
	require.Equal(t, "alice", principal.Principal)
	require.Equal(t, []string{"tenant"}, principal.Tenants)
	request.SetBasicAuth("alice", "wrong-password")

	for _, scenario := range []struct {
		name   string
		values []string
	}{
		{"missing", nil}, {"malformed", []string{"Basic bad!"}}, {"invalid", request.Header.Values("Authorization")},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			query := &qdata.Query{Header: map[string]*qdata.HeaderValues{
				"Authorization": {Values: scenario.values},
			}}
			_, err := authenticate(t.Context(), query)
			require.Equal(t, qerror.CodeUnauthenticated, qerror.CodeOf(err))
			require.NotContains(t, err.Error(), "wrong-password")

			header := make(http.Header)
			qerror.SetAuthenticationChallenge(header, err)
			require.Equal(t, `Basic realm="Queries"`, header.Get("WWW-Authenticate"))
		})
	}
}
