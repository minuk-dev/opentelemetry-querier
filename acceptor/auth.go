package acceptor

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/component/extension"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// Principal carries a verified identity and the tenant scope issued by its authenticator.
type Principal struct {
	qdata.Identity

	Tenants []string
}

// ServerAuthenticator verifies transport credentials before the query pipeline.
type ServerAuthenticator interface {
	extension.Extension

	Authenticate(ctx context.Context, headers http.Header) (Principal, error)
}

// AuthenticatorFunc adapts a verifier that needs no lifecycle hooks.
type AuthenticatorFunc func(context.Context, http.Header) (Principal, error)

// Start is a no-op.
func (AuthenticatorFunc) Start(context.Context, component.Host) error { return nil }

// Shutdown is a no-op.
func (AuthenticatorFunc) Shutdown(context.Context) error { return nil }

// Authenticate verifies transport credentials.
func (verify AuthenticatorFunc) Authenticate(ctx context.Context, headers http.Header) (Principal, error) {
	return verify(ctx, headers)
}

// AuthConfig references a service extension by ID.
type AuthConfig struct {
	Authenticator string `mapstructure:"authenticator"`
	TenantHeader  string `mapstructure:"tenant_header"`
}

// WithAuthentication authenticates the sanitized transport headers and binds the selected tenant.
func WithAuthentication(
	next pipeline.Handler, verifier ServerAuthenticator, tenantHeader string, timeout time.Duration,
) pipeline.HandlerFunc {
	if tenantHeader == "" {
		tenantHeader = "X-Scope-OrgID"
	}

	return func(ctx context.Context, query *qdata.Query) (*qdata.Result, error) {
		if timeout > 0 {
			var cancel context.CancelFunc

			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		authenticated, err := authenticate(ctx, verifier, query, tenantHeader)
		if err != nil {
			if observer, ok := next.(interface {
				AuthenticationFailed(ctx context.Context, err error)
			}); ok {
				observer.AuthenticationFailed(ctx, err)
			}

			return nil, err
		}

		result, err := next.Handle(authenticated, query)
		if err != nil {
			return nil, fmt.Errorf("handle authenticated query: %w", err)
		}

		return result, nil
	}
}

func authenticate(
	ctx context.Context, verifier ServerAuthenticator, query *qdata.Query, tenantHeader string,
) (context.Context, error) {
	if ctx.Err() != nil {
		return ctx, qerror.UpstreamTransportError(ctx.Err())
	}

	headers := make(http.Header, len(query.GetHeader()))
	for name, values := range query.GetHeader() {
		for _, value := range values.GetValues() {
			headers.Add(name, value)
		}
	}

	principal, err := verifier.Authenticate(ctx, headers)
	if ctx.Err() != nil {
		return ctx, qerror.UpstreamTransportError(ctx.Err())
	}

	if err != nil {
		return ctx, fmt.Errorf("authenticate query: %w", err)
	}

	if principal.Principal == "" || principal.Authority == "" {
		return ctx, qerror.New(qerror.CodeUnauthenticated, "missing verified principal")
	}

	tenant, err := selectTenant(headers.Values(tenantHeader), principal.Tenants)
	if err != nil {
		return ctx, err
	}

	principal.Tenant = tenant
	qdata.SetTenantID(query, tenant)

	return qdata.WithIdentity(ctx, principal.Identity), nil
}

func selectTenant(values, allowed []string) (string, error) {
	var tenant string

	switch {
	case len(values) > 1:
		return "", qerror.New(qerror.CodePermissionDenied, "ambiguous tenant selection")
	case len(values) == 1:
		tenant = values[0]
	case len(allowed) == 1:
		tenant = allowed[0]
	}

	if tenant == "" || !slices.Contains(allowed, tenant) {
		return "", qerror.New(qerror.CodePermissionDenied, "tenant outside verified scope")
	}

	return tenant, nil
}
