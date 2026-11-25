package pipeline

import (
	"context"
	"slices"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// Principal is a verified identity and the set of tenants it may select.
type Principal struct {
	qdata.Identity
	Tenants []string
}

// Authenticator is invoked before any request processor or backend.
type Authenticator func(context.Context, *qdata.Query) (Principal, error)

func (p *Pipeline) authorizeIdentity(ctx context.Context, query *qdata.Query) (context.Context, error) {
	inherited, hasInherited := qdata.IdentityFromContext(ctx)
	if p.Authenticate == nil {
		if hasInherited {
			qdata.SetTenantID(query, inherited.Tenant)
		}
		return ctx, nil
	}

	principal, err := p.Authenticate(ctx, query)
	if err != nil {
		return ctx, err
	}
	if principal.Principal == "" || principal.Authority == "" {
		return ctx, qerror.New(qerror.CodeUnauthenticated, "missing verified principal")
	}

	header := p.TenantHeader
	if header == "" {
		header = "X-Scope-OrgID"
	}
	values := qdata.HeaderValuesFor(query, header)
	if len(values) > 1 {
		return ctx, qerror.New(qerror.CodePermissionDenied, "ambiguous tenant selection")
	}
	var tenant string
	switch {
	case hasInherited:
		tenant = inherited.Tenant
	case len(values) == 1:
		tenant = values[0]
	case len(principal.Tenants) == 1:
		tenant = principal.Tenants[0]
	}
	if tenant == "" || !slices.Contains(principal.Tenants, tenant) {
		return ctx, qerror.New(qerror.CodePermissionDenied, "tenant outside verified scope")
	}

	if hasInherited {
		if inherited.Principal != principal.Principal || inherited.Authority != principal.Authority {
			return ctx, qerror.New(qerror.CodePermissionDenied, "leaf identity differs from parent identity")
		}
		qdata.SetTenantID(query, inherited.Tenant)
		return ctx, nil
	}

	principal.Tenant = tenant
	qdata.SetTenantID(query, tenant)
	return qdata.WithIdentity(ctx, principal.Identity), nil
}
