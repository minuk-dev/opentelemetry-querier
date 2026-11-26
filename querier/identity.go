package querier

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

const minGatewaySecretBytes = 32

var (
	errMultipleIdentityVerifiers = errors.New("configure one identity verifier per pipeline")
	errGatewayIdentityConfig     = errors.New("gateway identity requires authority and secret_file")
	errGatewaySecretSize         = errors.New("gateway identity secret must be at least 32 bytes")
)

// IdentityConfig configures identity for one query pipeline.
type IdentityConfig struct {
	Basic        *BasicIdentityConfig   `mapstructure:"basic"`
	TenantHeader string                 `mapstructure:"tenant_header"`
	Gateway      *GatewayIdentityConfig `mapstructure:"gateway"`
}

// GatewayIdentityConfig trusts assertions authenticated by a dedicated gateway secret.
type GatewayIdentityConfig struct {
	Authority  string `mapstructure:"authority"`
	SecretFile string `mapstructure:"secret_file"`
}

func configureIdentity(pipe *pipeline.Pipeline, cfg IdentityConfig) error {
	pipe.TenantHeader = cfg.TenantHeader
	if cfg.Basic != nil {
		if cfg.Gateway != nil {
			return errMultipleIdentityVerifiers
		}

		authenticate, err := basicAuthenticator(*cfg.Basic)
		if err != nil {
			return err
		}

		pipe.Authenticate = authenticate

		return nil
	}

	if cfg.Gateway != nil {
		authenticate, err := gatewayAuthenticator(*cfg.Gateway)
		if err != nil {
			return err
		}

		pipe.Authenticate = authenticate
	}

	return nil
}

func gatewayAuthenticator(cfg GatewayIdentityConfig) (pipeline.Authenticator, error) {
	if cfg.Authority == "" || cfg.SecretFile == "" {
		return nil, errGatewayIdentityConfig
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

	return func(_ context.Context, query *qdata.Query) (pipeline.Principal, error) {
		supplied := qdata.HeaderValuesFor(query, "X-Querier-Gateway-Secret")
		if len(supplied) != 1 {
			return pipeline.Principal{}, qerror.New(qerror.CodeUnauthenticated, "missing gateway credentials")
		}

		actual := sha256.Sum256([]byte(supplied[0]))
		if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			return pipeline.Principal{}, qerror.New(qerror.CodeUnauthenticated, "invalid gateway credentials")
		}

		subjects := qdata.HeaderValuesFor(query, "X-Querier-Principal")
		if len(subjects) != 1 || strings.TrimSpace(subjects[0]) == "" {
			return pipeline.Principal{}, qerror.New(qerror.CodeUnauthenticated, "invalid gateway principal")
		}

		return pipeline.Principal{
			Identity: qdata.Identity{
				Authority: cfg.Authority, Principal: subjects[0], Tenant: "", Scopes: nil,
				Groups: qdata.HeaderValuesFor(query, "X-Querier-Group"),
				Roles:  qdata.HeaderValuesFor(query, "X-Querier-Role"),
			},
			Tenants: qdata.HeaderValuesFor(query, "X-Querier-Allowed-Tenant"),
		}, nil
	}, nil
}
