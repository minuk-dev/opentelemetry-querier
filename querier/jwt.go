package querier

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// JWTIdentityConfig describes one issuer and resource, with an RSA key or JWKS.
type JWTIdentityConfig struct {
	Issuer        string   `mapstructure:"issuer"`
	Audience      string   `mapstructure:"audience"`
	Algorithms    []string `mapstructure:"algorithms"`
	PublicKeyFile string   `mapstructure:"public_key_file"`
	JWKSURL       string   `mapstructure:"jwks_url"`
	TenantClaim   string   `mapstructure:"tenant_claim"`
	GroupClaim    string   `mapstructure:"group_claim"`
	RoleClaim     string   `mapstructure:"role_claim"`
}

const (
	jwksRequestTimeout   = 3 * time.Second
	minRSAKeyBits        = 2048
	maxJWKKeyBits        = 8192
	maxBearerHeaderBytes = 16384
	jwksRefreshInterval  = 15 * time.Second
	jwksKeyLifetime      = 5 * time.Minute
)

var (
	errJWTConfig         = errors.New("JWT requires an issuer, audience, algorithm, tenant claim and key source")
	errJWTAlgorithm      = errors.New("unsupported JWT algorithm")
	errJWTKeySize        = errors.New("JWT key must be RSA with at least 2048 bits")
	errJWKSURL           = errors.New("JWKS requires an HTTPS URL without credentials")
	errJWTKeyIdentifier  = errors.New("missing JWT key identifier")
	errJWTKeyUnavailable = errors.New("JWT verification key unavailable")
	errJWTUnknownKey     = errors.New("unknown JWT verification key")
	errJWKSRequest       = errors.New("JWKS request failed")
	errJWKSResponseSize  = errors.New("invalid JWKS response size")
	errJWKSInvalidKey    = errors.New("invalid or duplicate RSA verification key")
	errJWKSEmpty         = errors.New("no usable JWKS verification keys")
)

func jwtAuthenticator(cfg JWTIdentityConfig) (pipeline.Authenticator, error) {
	err := validateJWTConfig(cfg)
	if err != nil {
		return nil, err
	}

	keys, err := newJWTKeySource(cfg)
	if err != nil {
		return nil, err
	}

	parser := jwt.NewParser(jwt.WithValidMethods(cfg.Algorithms), jwt.WithIssuer(cfg.Issuer),
		jwt.WithAudience(cfg.Audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt())

	return func(ctx context.Context, query *qdata.Query) (pipeline.Principal, error) {
		value := bearerToken(query)
		if value == "" {
			return pipeline.Principal{}, qerror.New(qerror.CodeUnauthenticated, "missing Bearer credentials")
		}

		claims := jwt.MapClaims{}

		token, err := parser.ParseWithClaims(value, claims, func(token *jwt.Token) (any, error) {
			return keys.lookup(ctx, token)
		})
		if err != nil || !token.Valid {
			return pipeline.Principal{}, qerror.New(qerror.CodeUnauthenticated, "invalid Bearer credentials")
		}

		return principalFromJWT(cfg, claims)
	}, nil
}

func validateJWTConfig(cfg JWTIdentityConfig) error {
	if cfg.Issuer == "" || cfg.Audience == "" || len(cfg.Algorithms) == 0 || cfg.TenantClaim == "" ||
		(cfg.PublicKeyFile == "") == (cfg.JWKSURL == "") {
		return errJWTConfig
	}

	for _, algorithm := range cfg.Algorithms {
		if !slices.Contains([]string{"RS256", "RS384", "RS512"}, algorithm) {
			return errJWTAlgorithm
		}
	}

	return nil
}

type jwtKeySource struct {
	pinned *rsa.PublicKey
	remote *jwksCache
}

func newJWTKeySource(cfg JWTIdentityConfig) (jwtKeySource, error) {
	var source jwtKeySource

	if cfg.PublicKeyFile != "" {
		data, err := os.ReadFile(cfg.PublicKeyFile)
		if err != nil {
			return source, fmt.Errorf("JWT public key file: %w", err)
		}

		key, err := jwt.ParseRSAPublicKeyFromPEM(data)
		if err != nil || key.N.BitLen() < minRSAKeyBits {
			return source, errJWTKeySize
		}

		source.pinned = key

		return source, nil
	}

	endpoint, err := url.Parse(cfg.JWKSURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil {
		return source, errJWKSURL
	}

	cache := new(jwksCache)
	cache.url = cfg.JWKSURL
	cache.now = time.Now
	cache.client = &http.Client{
		Timeout:       jwksRequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	source.remote = cache

	return source, nil
}

func (source jwtKeySource) lookup(ctx context.Context, token *jwt.Token) (any, error) {
	if source.pinned != nil {
		return source.pinned, nil
	}

	return source.remote.lookup(ctx, token)
}

func bearerToken(query *qdata.Query) string {
	values := qdata.HeaderValuesFor(query, "Authorization")
	if len(values) != 1 || len(values[0]) > maxBearerHeaderBytes {
		return ""
	}

	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}

	return parts[1]
}

func principalFromJWT(cfg JWTIdentityConfig, claims jwt.MapClaims) (pipeline.Principal, error) {
	subject, err := claims.GetSubject()
	tenants, tenantsOK := identityClaim(claims, cfg.TenantClaim)
	groups, groupsOK := identityClaim(claims, cfg.GroupClaim)

	roles, rolesOK := identityClaim(claims, cfg.RoleClaim)
	if err != nil || strings.TrimSpace(subject) == "" || len(tenants) == 0 || !tenantsOK || !groupsOK || !rolesOK {
		return pipeline.Principal{}, qerror.New(qerror.CodeUnauthenticated, "invalid JWT identity claims")
	}

	return pipeline.Principal{Identity: qdata.Identity{
		Authority: cfg.Issuer, Principal: subject, Tenant: "", Groups: groups, Roles: roles, Scopes: nil,
	}, Tenants: tenants}, nil
}

func identityClaim(claims jwt.MapClaims, name string) ([]string, bool) {
	if name == "" {
		return nil, true
	}

	raw, exists := claims[name]
	if !exists {
		return nil, true
	}

	switch values := raw.(type) {
	case string:
		if strings.TrimSpace(values) != "" {
			return []string{values}, true
		}
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			entry, ok := value.(string)
			if !ok || strings.TrimSpace(entry) == "" {
				return nil, false
			}

			out = append(out, entry)
		}

		return out, true
	}

	return nil, false
}

type jwksCache struct {
	mu           sync.Mutex
	url          string
	client       *http.Client
	now          func() time.Time
	refreshAfter time.Time
	expires      time.Time
	keys         map[string]*rsa.PublicKey
	refreshing   bool
}

func (c *jwksCache) lookup(ctx context.Context, token *jwt.Token) (any, error) {
	id, ok := token.Header["kid"].(string)
	if !ok || id == "" {
		return nil, errJWTKeyIdentifier
	}

	now := c.now()
	c.mu.Lock()
	if key := c.keys[id]; key != nil && now.Before(c.expires) {
		c.mu.Unlock()

		return key, nil
	}

	if c.refreshing || now.Before(c.refreshAfter) {
		c.mu.Unlock()

		return nil, errJWTKeyUnavailable
	}

	c.refreshing = true
	c.refreshAfter = now.Add(jwksRefreshInterval)
	c.mu.Unlock()

	keys, err := c.fetch(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()

	c.refreshing = false

	if err != nil {
		return nil, err
	}

	c.keys = keys
	c.expires = c.now().Add(jwksKeyLifetime)

	if keys[id] == nil {
		return nil, errJWTUnknownKey
	}

	return keys[id], nil
}

func (c *jwksCache) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, fmt.Errorf("create JWKS request: %w", err)
	}

	response, err := c.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, errJWKSRequest
	}

	const maxJWKSBytes = 1 << 20

	data, err := io.ReadAll(io.LimitReader(response.Body, maxJWKSBytes+1))
	if err != nil || len(data) > maxJWKSBytes {
		return nil, errJWKSResponseSize
	}

	return decodeJWKS(data)
}

type jwkEntry struct {
	ID       string `json:"kid"`
	Type     string `json:"kty"`
	Use      string `json:"use"`
	Modulus  string `json:"n"`
	Exponent string `json:"e"`
}

func decodeJWKS(data []byte) (map[string]*rsa.PublicKey, error) {
	var document struct {
		Keys []jwkEntry `json:"keys"`
	}

	err := json.Unmarshal(data, &document)
	if err != nil {
		return nil, fmt.Errorf("decode JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey)

	for _, entry := range document.Keys {
		if !entry.signingRSA() {
			continue
		}

		if keys[entry.ID] != nil {
			return nil, errJWKSInvalidKey
		}

		key, err := entry.rsaKey()
		if err != nil {
			return nil, err
		}

		keys[entry.ID] = key
	}

	if len(keys) == 0 {
		return nil, errJWKSEmpty
	}

	return keys, nil
}

func (entry jwkEntry) signingRSA() bool {
	return entry.Type == "RSA" && (entry.Use == "" || entry.Use == "sig") && entry.ID != ""
}

func (entry jwkEntry) rsaKey() (*rsa.PublicKey, error) {
	modulus, modErr := base64.RawURLEncoding.DecodeString(entry.Modulus)
	exponent, expErr := base64.RawURLEncoding.DecodeString(entry.Exponent)

	number := new(big.Int).SetBytes(modulus)
	if modErr != nil || expErr != nil || number.BitLen() < minRSAKeyBits || number.BitLen() > maxJWKKeyBits {
		return nil, errJWKSInvalidKey
	}

	power := new(big.Int).SetBytes(exponent)
	if !power.IsInt64() || power.Int64() < 3 || power.Int64() > 1<<31-1 || power.Bit(0) == 0 {
		return nil, errJWKSInvalidKey
	}

	return &rsa.PublicKey{N: number, E: int(power.Int64())}, nil
}
