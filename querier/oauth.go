package querier

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// OAuthIdentityConfig supports RFC 9068 JWTs or opaque tokens via introspection.
type OAuthIdentityConfig struct {
	JWT            *JWTIdentityConfig   `mapstructure:"jwt"`
	Introspection  *IntrospectionConfig `mapstructure:"introspection"`
	RequiredScopes []string             `mapstructure:"required_scopes"`
}

// IntrospectionConfig describes the trusted OAuth token endpoint and resource.
type IntrospectionConfig struct {
	Endpoint         string        `mapstructure:"endpoint"`
	Issuer           string        `mapstructure:"issuer"`
	Audience         string        `mapstructure:"audience"`
	TenantClaim      string        `mapstructure:"tenant_claim"`
	ClientID         string        `mapstructure:"client_id"`
	ClientSecretFile string        `mapstructure:"client_secret_file"`
	Timeout          time.Duration `mapstructure:"timeout"`
}

func oauthAuthenticator(cfg OAuthIdentityConfig) (pipeline.Authenticator, error) {
	var authenticate pipeline.Authenticator
	var err error
	switch {
	case cfg.JWT != nil && cfg.Introspection == nil:
		jwtCfg := *cfg.JWT
		authenticate, err = newJWTAuthenticator(jwtCfg, func(token *jwt.Token, claims jwt.MapClaims) (pipeline.Principal, error) {
			typ, _ := token.Header["typ"].(string)
			issuedAt, issuedErr := claims.GetIssuedAt()
			clientID, _ := claims["client_id"].(string)
			tokenID, _ := claims["jti"].(string)
			if !(strings.EqualFold(typ, "at+jwt") || strings.EqualFold(typ, "application/at+jwt")) ||
				issuedErr != nil || issuedAt == nil || clientID == "" || tokenID == "" {
				return pipeline.Principal{}, qerror.New(qerror.CodeUnauthenticated, "invalid JWT access token profile")
			}
			return oauthPrincipal(jwtCfg, claims)
		})
	case cfg.JWT == nil && cfg.Introspection != nil:
		authenticate, err = introspectionAuthenticator(*cfg.Introspection)
	default:
		return nil, errors.New("configure one OAuth token mode")
	}
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, query *qdata.Query) (pipeline.Principal, error) {
		principal, err := authenticate(ctx, query)
		if err != nil {
			return principal, err
		}
		for _, scope := range cfg.RequiredScopes {
			if !slices.Contains(principal.Scopes, scope) {
				return pipeline.Principal{}, qerror.New(qerror.CodePermissionDenied, "required OAuth scope missing")
			}
		}
		return principal, nil
	}, nil
}

func oauthPrincipal(cfg JWTIdentityConfig, claims jwt.MapClaims) (pipeline.Principal, error) {
	principal, err := principalFromJWT(cfg, claims)
	if err != nil {
		return principal, err
	}
	if scope, exists := claims["scope"]; exists {
		value, ok := scope.(string)
		if !ok {
			return pipeline.Principal{}, qerror.New(qerror.CodeUnauthenticated, "invalid OAuth scope claim")
		}
		principal.Scopes = strings.Fields(value)
	}
	return principal, nil
}

func introspectionAuthenticator(cfg IntrospectionConfig) (pipeline.Authenticator, error) {
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil ||
		cfg.Issuer == "" || cfg.Audience == "" || cfg.TenantClaim == "" || cfg.ClientID == "" || cfg.ClientSecretFile == "" {
		return nil, errors.New("introspection requires HTTPS endpoint, issuer, audience, tenant_claim and client credentials")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 3 * time.Second
	}
	if cfg.Timeout < 0 || cfg.Timeout > 30*time.Second {
		return nil, errors.New("introspection timeout must be at most 30s")
	}
	data, err := os.ReadFile(cfg.ClientSecretFile)
	if err != nil {
		return nil, err
	}
	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return nil, errors.New("introspection client secret is empty")
	}
	client := &http.Client{Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context, query *qdata.Query) (pipeline.Principal, error) {
		return introspect(ctx, client, cfg, secret, bearerToken(query))
	}, nil
}

func introspect(ctx context.Context, client *http.Client, cfg IntrospectionConfig, secret, token string) (pipeline.Principal, error) {
	if token == "" {
		return pipeline.Principal{}, qerror.New(qerror.CodeUnauthenticated, "missing Bearer credentials")
	}
	unavailable := qerror.New(qerror.CodeUnavailable, "token introspection unavailable")
	values := url.Values{"token": {token}, "token_type_hint": {"access_token"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return pipeline.Principal{}, unavailable
	}
	request.SetBasicAuth(url.QueryEscape(cfg.ClientID), url.QueryEscape(secret))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return pipeline.Principal{}, unavailable
	}
	defer response.Body.Close()
	const maxBytes = 1 << 20
	if response.StatusCode != http.StatusOK {
		return pipeline.Principal{}, unavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		return pipeline.Principal{}, unavailable
	}
	claims := jwt.MapClaims{}
	if err := json.Unmarshal(data, &claims); err != nil {
		return pipeline.Principal{}, unavailable
	}
	active, ok := claims["active"].(bool)
	if !ok {
		return pipeline.Principal{}, unavailable
	}
	validator := jwt.NewValidator(jwt.WithIssuer(cfg.Issuer), jwt.WithAudience(cfg.Audience), jwt.WithExpirationRequired())
	if !active || validator.Validate(claims) != nil {
		return pipeline.Principal{}, qerror.New(qerror.CodeUnauthenticated, "inactive or invalid access token")
	}
	return oauthPrincipal(JWTIdentityConfig{Issuer: cfg.Issuer, TenantClaim: cfg.TenantClaim}, claims)
}
