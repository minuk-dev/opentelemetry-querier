package querier

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
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

func jwtAuthenticator(cfg JWTIdentityConfig) (pipeline.Authenticator, error) {
	return newJWTAuthenticator(cfg, func(_ *jwt.Token, claims jwt.MapClaims) (pipeline.Principal, error) {
		return principalFromJWT(cfg, claims)
	})
}

func newJWTAuthenticator(cfg JWTIdentityConfig, read func(*jwt.Token, jwt.MapClaims) (pipeline.Principal, error)) (pipeline.Authenticator, error) {
	if cfg.Issuer == "" || cfg.Audience == "" || len(cfg.Algorithms) == 0 || cfg.TenantClaim == "" ||
		(cfg.PublicKeyFile == "") == (cfg.JWKSURL == "") {
		return nil, errors.New("JWT requires issuer, audience, algorithms, tenant_claim and exactly one verification key source")
	}
	for _, alg := range cfg.Algorithms {
		if !slices.Contains([]string{"RS256", "RS384", "RS512"}, alg) {
			return nil, errors.New("unsupported JWT algorithm")
		}
	}
	var lookup func(context.Context, *jwt.Token) (any, error)
	if cfg.PublicKeyFile != "" {
		data, err := os.ReadFile(cfg.PublicKeyFile)
		if err != nil {
			return nil, err
		}
		key, err := jwt.ParseRSAPublicKeyFromPEM(data)
		if err != nil || key.N.BitLen() < 2048 {
			return nil, errors.New("JWT key must be RSA with at least 2048 bits")
		}
		lookup = func(context.Context, *jwt.Token) (any, error) { return key, nil }
	} else {
		endpoint, err := url.Parse(cfg.JWKSURL)
		if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil {
			return nil, errors.New("JWKS requires an HTTPS URL without credentials")
		}
		keys := &jwksCache{
			url: cfg.JWKSURL, now: time.Now,
			client: &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		}
		lookup = keys.lookup
	}
	parser := jwt.NewParser(jwt.WithValidMethods(cfg.Algorithms), jwt.WithIssuer(cfg.Issuer),
		jwt.WithAudience(cfg.Audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	return func(ctx context.Context, query *qdata.Query) (pipeline.Principal, error) {
		value := bearerToken(query)
		if value == "" {
			return pipeline.Principal{}, qerror.New(qerror.CodeUnauthenticated, "missing Bearer credentials")
		}
		claims := jwt.MapClaims{}
		token, err := parser.ParseWithClaims(value, claims, func(token *jwt.Token) (any, error) { return lookup(ctx, token) })
		if err != nil || !token.Valid {
			return pipeline.Principal{}, qerror.New(qerror.CodeUnauthenticated, "invalid Bearer credentials")
		}
		return read(token, claims)
	}, nil
}

func bearerToken(query *qdata.Query) string {
	values := qdata.HeaderValuesFor(query, "Authorization")
	if len(values) != 1 || len(values[0]) > 16384 {
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
		Authority: cfg.Issuer, Principal: subject, Groups: groups, Roles: roles,
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
		return nil, errors.New("missing JWT key identifier")
	}
	now := c.now()
	c.mu.Lock()
	if key := c.keys[id]; key != nil && now.Before(c.expires) {
		c.mu.Unlock()
		return key, nil
	}
	if c.refreshing || now.Before(c.refreshAfter) {
		c.mu.Unlock()
		return nil, errors.New("JWT verification key unavailable")
	}
	c.refreshing = true
	c.refreshAfter = now.Add(15 * time.Second)
	c.mu.Unlock()

	keys, err := c.fetch(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshing = false
	if err != nil {
		return nil, err
	}
	c.keys = keys
	c.expires = c.now().Add(5 * time.Minute)
	if keys[id] == nil {
		return nil, errors.New("unknown JWT verification key")
	}
	return keys[id], nil
}

func (c *jwksCache) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	const maxJWKSBytes = 1 << 20
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("JWKS request failed")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxJWKSBytes+1))
	if err != nil || len(data) > maxJWKSBytes {
		return nil, errors.New("invalid JWKS response size")
	}
	var document struct {
		Keys []struct {
			ID       string `json:"kid"`
			Type     string `json:"kty"`
			Use      string `json:"use"`
			Modulus  string `json:"n"`
			Exponent string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	keys := make(map[string]*rsa.PublicKey)
	for _, entry := range document.Keys {
		if entry.Type != "RSA" || (entry.Use != "" && entry.Use != "sig") || entry.ID == "" {
			continue
		}
		modulus, modErr := base64.RawURLEncoding.DecodeString(entry.Modulus)
		exponent, expErr := base64.RawURLEncoding.DecodeString(entry.Exponent)
		n := new(big.Int).SetBytes(modulus)
		e := new(big.Int).SetBytes(exponent)
		if modErr != nil || expErr != nil || n.BitLen() < 2048 || n.BitLen() > 8192 || !e.IsInt64() || e.Int64() < 3 ||
			e.Int64() > 1<<31-1 || e.Bit(0) == 0 || keys[entry.ID] != nil {
			return nil, errors.New("invalid or duplicate RSA verification key")
		}
		keys[entry.ID] = &rsa.PublicKey{N: n, E: int(e.Int64())}
	}
	if len(keys) == 0 {
		return nil, errors.New("no usable JWKS verification keys")
	}
	return keys, nil
}
