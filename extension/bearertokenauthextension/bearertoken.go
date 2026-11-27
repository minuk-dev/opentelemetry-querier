// Package bearertokenauthextension authenticates static bearer credentials.
package bearertokenauthextension

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/minuk-dev/opentelemetry-querier/acceptor"
	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/component/extension"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

var errConfig = errors.New("bearertokenauth: authority, principal, tenants and nonempty tokens are required")

// Config binds all configured bearer tokens to one service identity.
type Config struct {
	Authority string   `mapstructure:"authority"`
	Principal string   `mapstructure:"principal"`
	Tenants   []string `mapstructure:"tenants"`
	Tokens    []string `mapstructure:"tokens"`
}

// NewFactory registers the static bearer authenticator.
func NewFactory() extension.Factory {
	return extension.NewFactory(
		component.MustNewType("bearertokenauth"), func() component.Config { return new(Config) }, create,
	)
}

func create(_ context.Context, _ component.Settings, cfg component.Config) (extension.Extension, error) {
	config, ok := cfg.(*Config)
	if !ok || config.Authority == "" || config.Principal == "" || len(config.Tenants) == 0 || len(config.Tokens) == 0 {
		return nil, errConfig
	}

	hashes := make([][sha256.Size]byte, 0, len(config.Tokens))
	for _, token := range config.Tokens {
		if token == "" || strings.ContainsAny(token, " \t\r\n") {
			return nil, errConfig
		}

		hashes = append(hashes, sha256.Sum256([]byte(token)))
	}

	identity := qdata.Identity{
		Authority: config.Authority, Principal: config.Principal, Tenant: "", Groups: nil, Roles: nil, Scopes: nil,
	}

	return acceptor.AuthenticatorFunc(func(_ context.Context, headers http.Header) (acceptor.Principal, error) {
		if validToken(headers.Values("Authorization"), hashes) {
			return acceptor.Principal{Identity: identity, Tenants: config.Tenants}, nil
		}

		return acceptor.Principal{}, qerror.New(qerror.CodeUnauthenticated, "missing or invalid Bearer credentials")
	}), nil
}

func validToken(values []string, hashes [][sha256.Size]byte) bool {
	if len(values) != 1 {
		return false
	}

	scheme, token, found := strings.Cut(values[0], " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return false
	}

	actual := sha256.Sum256([]byte(token))

	matches := 0
	for _, expected := range hashes {
		matches |= subtle.ConstantTimeCompare(actual[:], expected[:])
	}

	return matches == 1
}
