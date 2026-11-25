package querier

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
	publicKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&key.PublicKey)})
	file := filepath.Join(t.TempDir(), "public.pem")
	require.NoError(t, os.WriteFile(file, publicKey, 0600))
	auth, err := jwtAuthenticator(JWTIdentityConfig{Issuer: "issuer", Audience: "querier", Algorithms: []string{"RS256"}, TenantClaim: "tenant", GroupClaim: "groups", PublicKeyFile: file})
	require.NoError(t, err)
	now := time.Now()
	for _, tc := range []struct {
		name   string
		update func(jwt.MapClaims)
		allow  bool
	}{
		{"valid", func(jwt.MapClaims) {}, true},
		{"wrong audience", func(c jwt.MapClaims) { c["aud"] = "other" }, false},
		{"wrong issuer", func(c jwt.MapClaims) { c["iss"] = "other" }, false},
		{"expired", func(c jwt.MapClaims) { c["exp"] = now.Add(-time.Minute).Unix() }, false},
		{"not yet valid", func(c jwt.MapClaims) { c["nbf"] = now.Add(time.Minute).Unix() }, false},
		{"malformed group", func(c jwt.MapClaims) { c["groups"] = 12 }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := jwt.MapClaims{"iss": "issuer", "aud": "querier", "sub": "alice", "exp": now.Add(time.Minute).Unix(), "tenant": "team-a", "groups": []string{"readers"}}
			tc.update(claims)
			token, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
			require.NoError(t, err)
			query := &qdata.Query{Header: map[string]*qdata.HeaderValues{"Authorization": {Values: []string{"Bearer " + token}}}}
			principal, err := auth(t.Context(), query)
			if !tc.allow {
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
