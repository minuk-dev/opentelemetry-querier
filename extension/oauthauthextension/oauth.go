// Package oauthauthextension authenticates OAuth access tokens.
package oauthauthextension

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/minuk-dev/opentelemetry-querier/acceptor"
	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/component/extension"
	"github.com/minuk-dev/opentelemetry-querier/extension/jwtauthextension"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// Config supports RFC 9068 JWTs or opaque tokens via introspection.
type Config struct {
	JWT           *jwtauthextension.Config `mapstructure:"jwt"`
	Introspection *IntrospectionConfig     `mapstructure:"introspection"`
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

const (
	introspectionDefaultTimeout = 3 * time.Second
	introspectionMaxTimeout     = 30 * time.Second
)

var (
	errOAuthTokenMode            = errors.New("configure one OAuth token mode")
	errIntrospectionEndpoint     = errors.New("introspection requires an HTTPS endpoint without credentials")
	errIntrospectionConfig       = errors.New("introspection requires issuer, audience, tenant claim and credentials")
	errIntrospectionTimeout      = errors.New("introspection timeout must be at most 30s")
	errIntrospectionClientSecret = errors.New("introspection client secret is empty")
)

func oauthAuthenticator(cfg Config) (acceptor.AuthenticatorFunc, error) {
	switch {
	case cfg.JWT != nil && cfg.Introspection == nil:
		jwtCfg := *cfg.JWT

		claimsReader := func(token *jwt.Token, claims jwt.MapClaims) (acceptor.Principal, error) {
			if !validJWTAccessToken(token, claims) {
				return acceptor.Principal{}, qerror.New(qerror.CodeUnauthenticated, "invalid JWT access token profile")
			}

			return oauthPrincipal(jwtCfg, claims)
		}

		authenticator, err := jwtauthextension.NewAuthenticator(jwtCfg, claimsReader)
		if err != nil {
			return nil, fmt.Errorf("initialize JWT access token verifier: %w", err)
		}

		return authenticator, nil
	case cfg.JWT == nil && cfg.Introspection != nil:
		return introspectionAuthenticator(*cfg.Introspection)
	default:
		return nil, errOAuthTokenMode
	}
}

func validJWTAccessToken(token *jwt.Token, claims jwt.MapClaims) bool {
	mediaType, _ := token.Header["typ"].(string)
	issuedAt, issuedErr := claims.GetIssuedAt()
	clientID, _ := claims["client_id"].(string)
	tokenID, _ := claims["jti"].(string)

	return (strings.EqualFold(mediaType, "at+jwt") || strings.EqualFold(mediaType, "application/at+jwt")) &&
		issuedErr == nil && issuedAt != nil && clientID != "" && tokenID != ""
}

func oauthPrincipal(cfg jwtauthextension.Config, claims jwt.MapClaims) (acceptor.Principal, error) {
	principal, err := jwtauthextension.PrincipalFromClaims(cfg, claims)
	if err != nil {
		return principal, fmt.Errorf("resolve OAuth principal: %w", err)
	}

	if scope, exists := claims["scope"]; exists {
		value, ok := scope.(string)
		if !ok {
			return acceptor.Principal{}, qerror.New(qerror.CodeUnauthenticated, "invalid OAuth scope claim")
		}

		principal.Scopes = strings.Fields(value)
	}

	return principal, nil
}

func (cfg IntrospectionConfig) validate() error {
	endpoint, err := url.Parse(cfg.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil {
		return errIntrospectionEndpoint
	}

	if cfg.Issuer == "" || cfg.Audience == "" || cfg.TenantClaim == "" ||
		cfg.ClientID == "" || cfg.ClientSecretFile == "" {
		return errIntrospectionConfig
	}

	return nil
}

func introspectionAuthenticator(cfg IntrospectionConfig) (acceptor.AuthenticatorFunc, error) {
	err := cfg.validate()
	if err != nil {
		return nil, err
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = introspectionDefaultTimeout
	}

	if cfg.Timeout < 0 || cfg.Timeout > introspectionMaxTimeout {
		return nil, errIntrospectionTimeout
	}

	data, err := os.ReadFile(cfg.ClientSecretFile)
	if err != nil {
		return nil, fmt.Errorf("read introspection client secret: %w", err)
	}

	secret := strings.TrimSpace(string(data))
	if secret == "" {
		return nil, errIntrospectionClientSecret
	}

	client := &http.Client{
		Timeout:       cfg.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	return func(ctx context.Context, headers http.Header) (acceptor.Principal, error) {
		return introspect(ctx, client, cfg, secret, jwtauthextension.BearerToken(headers))
	}, nil
}

func introspect(
	ctx context.Context, client *http.Client, cfg IntrospectionConfig, secret, token string,
) (acceptor.Principal, error) {
	if token == "" {
		return acceptor.Principal{}, qerror.New(qerror.CodeUnauthenticated, "missing Bearer credentials")
	}

	claims, err := fetchIntrospection(ctx, client, cfg, secret, token)
	if err != nil {
		return acceptor.Principal{}, err
	}

	active, ok := claims["active"].(bool)
	if !ok {
		return acceptor.Principal{}, qerror.New(qerror.CodeUnavailable, "token introspection unavailable")
	}

	validator := jwt.NewValidator(jwt.WithIssuer(cfg.Issuer), jwt.WithAudience(cfg.Audience), jwt.WithExpirationRequired())
	if !active || validator.Validate(claims) != nil {
		return acceptor.Principal{}, qerror.New(qerror.CodeUnauthenticated, "inactive or invalid access token")
	}

	var identityConfig jwtauthextension.Config

	identityConfig.Issuer, identityConfig.TenantClaim = cfg.Issuer, cfg.TenantClaim

	return oauthPrincipal(identityConfig, claims)
}

func fetchIntrospection(
	ctx context.Context, client *http.Client, cfg IntrospectionConfig, secret, token string,
) (jwt.MapClaims, error) {
	unavailable := qerror.New(qerror.CodeUnavailable, "token introspection unavailable")
	values := url.Values{"token": {token}, "token_type_hint": {"access_token"}}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, unavailable
	}

	request.SetBasicAuth(url.QueryEscape(cfg.ClientID), url.QueryEscape(secret))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := client.Do(request)
	if err != nil {
		return nil, unavailable
	}

	defer func() { _ = response.Body.Close() }()

	const maxBytes = 1 << 20

	if response.StatusCode != http.StatusOK {
		return nil, unavailable
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		return nil, unavailable
	}

	claims := jwt.MapClaims{}

	err = json.Unmarshal(data, &claims)
	if err != nil {
		return nil, unavailable
	}

	return claims, nil
}

var errInvalidConfig = errors.New("oauthauth: invalid config type")

// NewFactory registers the oauthauth extension.
func NewFactory() extension.Factory {
	return extension.NewFactory(
		component.MustNewType("oauthauth"), func() component.Config { return new(Config) }, create,
	)
}

func create(_ context.Context, _ component.Settings, cfg component.Config) (extension.Extension, error) {
	config, ok := cfg.(*Config)
	if !ok {
		return nil, errInvalidConfig
	}

	return oauthAuthenticator(*config)
}
