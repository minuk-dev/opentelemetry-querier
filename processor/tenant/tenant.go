// Package tenant implements the tenant-control processor. It resolves the tenant
// for a query from a request header (Cortex/Mimir-style X-Scope-OrgID by
// default), optionally enforces its presence, and can register an enforced label
// matcher so the query-rewrite processor isolates the tenant's series.
package tenant

import (
	"context"
	"strings"

	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// DefaultHeader is the conventional multi-tenancy header in the Prometheus
// ecosystem.
const DefaultHeader = "X-Scope-OrgID"

// Config configures tenant resolution.
type Config struct {
	// Header is the request header carrying the tenant id.
	Header string `yaml:"header"`
	// Default is used when the header is absent; empty means no default.
	Default string `yaml:"default"`
	// Required rejects queries with no resolvable tenant.
	Required bool `yaml:"required"`
	// EnforceLabel, when set, registers an enforced equality matcher on this
	// label with the resolved tenant id, isolating the tenant's series.
	EnforceLabel string `yaml:"enforce_label"`
}

// Processor resolves and enforces the tenant.
type Processor struct {
	processor.Base
	cfg Config
}

// New builds the tenant processor.
func New(cfg Config) *Processor {
	if cfg.Header == "" {
		cfg.Header = DefaultHeader
	}
	return &Processor{cfg: cfg}
}

func (p *Processor) Name() string { return "tenant" }

// ProcessQuery resolves the tenant and, if configured, registers the isolation
// matcher.
func (p *Processor) ProcessQuery(_ context.Context, q *qdata.Query) error {
	tenant := q.GetTenantId()
	if tenant == "" {
		tenant = headerValue(q, p.cfg.Header)
	}
	if tenant == "" {
		tenant = p.cfg.Default
	}
	if tenant == "" {
		if p.cfg.Required {
			return qerror.New(qerror.CodeUnauthenticated, "tenant: missing %s header", p.cfg.Header)
		}
		return nil
	}

	q.TenantId = tenant
	if p.cfg.EnforceLabel != "" {
		q.EnforcedMatchers = append(q.EnforcedMatchers, &qdata.LabelMatcher{
			Name:  p.cfg.EnforceLabel,
			Op:    qdata.MatchEqual,
			Value: tenant,
		})
	}
	return nil
}

// headerValue does a case-insensitive lookup of the first value of a header.
func headerValue(q *qdata.Query, name string) string {
	for k, v := range q.GetHeader() {
		if strings.EqualFold(k, name) && len(v.GetValues()) > 0 {
			return v.GetValues()[0]
		}
	}
	return ""
}
