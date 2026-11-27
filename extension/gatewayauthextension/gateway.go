// Package gatewayauthextension verifies assertions from a trusted gateway.
package gatewayauthextension

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/minuk-dev/opentelemetry-querier/acceptor"
	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/component/extension"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

const minGatewaySecretBytes = 32

var (
	errConfig            = errors.New("gateway identity requires authority and secret_file")
	errGatewaySecretSize = errors.New("gateway identity secret must be at least 32 bytes")
)

// Config trusts assertions authenticated by a dedicated gateway secret.
type Config struct {
	Authority  string `mapstructure:"authority"`
	SecretFile string `mapstructure:"secret_file"`
}

func gatewayAuthenticator(cfg Config) (acceptor.AuthenticatorFunc, error) {
	if cfg.Authority == "" || cfg.SecretFile == "" {
		return nil, errConfig
	}

	secret, err := os.ReadFile(cfg.SecretFile)
	if err != nil {
		return nil, fmt.Errorf("gateway identity secret file: %w", err)
	}

	secret = []byte(strings.TrimSpace(string(secret)))
	if len(secret) < minGatewaySecretBytes {
		return nil, errGatewaySecretSize
	}

	expected := sha256.Sum256(secret)

	return func(_ context.Context, headers http.Header) (acceptor.Principal, error) {
		supplied := headers.Values("X-Querier-Gateway-Secret")
		if len(supplied) != 1 {
			return acceptor.Principal{}, qerror.New(qerror.CodeUnauthenticated, "missing gateway credentials")
		}

		actual := sha256.Sum256([]byte(supplied[0]))
		if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			return acceptor.Principal{}, qerror.New(qerror.CodeUnauthenticated, "invalid gateway credentials")
		}

		subjects := headers.Values("X-Querier-Principal")
		if len(subjects) != 1 || strings.TrimSpace(subjects[0]) == "" {
			return acceptor.Principal{}, qerror.New(qerror.CodeUnauthenticated, "invalid gateway principal")
		}

		return acceptor.Principal{
			Identity: qdata.Identity{
				Authority: cfg.Authority, Principal: subjects[0], Tenant: "", Scopes: nil,
				Groups: headers.Values("X-Querier-Group"),
				Roles:  headers.Values("X-Querier-Role"),
			},
			Tenants: headers.Values("X-Querier-Allowed-Tenant"),
		}, nil
	}, nil
}

var errInvalidConfig = errors.New("gateway authentication: invalid config type")

// NewFactory registers the trusted gateway authenticator.
func NewFactory() extension.Factory {
	return extension.NewFactory(
		component.MustNewType("gatewayauth"), func() component.Config { return new(Config) }, create,
	)
}

func create(_ context.Context, _ component.Settings, cfg component.Config) (extension.Extension, error) {
	config, ok := cfg.(*Config)
	if !ok {
		return nil, errInvalidConfig
	}

	return gatewayAuthenticator(*config)
}
