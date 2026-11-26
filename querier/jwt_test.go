package querier //nolint:testpackage // Exercises the internal JWT verifier and its claims mapping.

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
	"github.com/stretchr/testify/require"
)

func TestJWTAuthenticator(t *testing.T) {
	t.Parallel()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	publicKey := pem.EncodeToMemory(&pem.Block{
		Type: "RSA PUBLIC KEY", Headers: nil, Bytes: x509.MarshalPKCS1PublicKey(&key.PublicKey),
	})
	file := filepath.Join(t.TempDir(), "public.pem")
	require.NoError(t, os.WriteFile(file, publicKey, 0600))
	auth, err := jwtAuthenticator(JWTIdentityConfig{
		Issuer: "issuer", Audience: "querier", Algorithms: []string{"RS256"}, PublicKeyFile: file, JWKSURL: "",
		TenantClaim: "tenant", GroupClaim: "groups", RoleClaim: "",
	})
	require.NoError(t, err)

	now := time.Now()

	for _, scenario := range []struct {
		name   string
		update func(jwt.MapClaims)
		allow  bool
	}{
		{"valid", func(jwt.MapClaims) {}, true},
		{"wrong audience", func(claims jwt.MapClaims) { claims["aud"] = "other" }, false},
		{"wrong issuer", func(claims jwt.MapClaims) { claims["iss"] = "other" }, false},
		{"expired", func(claims jwt.MapClaims) { claims["exp"] = now.Add(-time.Minute).Unix() }, false},
		{"not yet valid", func(claims jwt.MapClaims) { claims["nbf"] = now.Add(time.Minute).Unix() }, false},
		{"malformed group", func(claims jwt.MapClaims) { claims["groups"] = 12 }, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			claims := jwt.MapClaims{
				"iss": "issuer", "aud": "querier", "sub": "alice", "exp": now.Add(time.Minute).Unix(),
				"tenant": "team-a", "groups": []string{"readers"},
			}
			scenario.update(claims)
			token, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
			require.NoError(t, err)

			query := &qdata.Query{Header: map[string]*qdata.HeaderValues{"Authorization": {Values: []string{"Bearer " + token}}}}

			principal, err := auth(t.Context(), query)
			if !scenario.allow {
				require.Error(t, err)
				require.Equal(t, qerror.CodeUnauthenticated, qerror.CodeOf(err))

				return
			}

			require.NoError(t, err)
			require.Equal(t, "alice", principal.Principal)
			require.Equal(t, []string{"team-a"}, principal.Tenants)
			require.Equal(t, []string{"readers"}, principal.Groups)
		})
	}
}
