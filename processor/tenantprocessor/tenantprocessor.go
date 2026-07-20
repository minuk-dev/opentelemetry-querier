// Package tenantprocessor implements the tenant-control processor. It resolves the tenant
// for a query from a request header (Cortex/Mimir-style X-Scope-OrgID by
// default), optionally enforces its presence, and can register an enforced label
// matcher so the query-rewrite processor isolates the tenant's series.
package tenantprocessor

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
	Header string `mapstructure:"header"`
	// Default is used when the header is absent; empty means no default.
	Default string `mapstructure:"default"`
	// Required rejects queries with no resolvable tenant.
	Required bool `mapstructure:"required"`
	// EnforceLabel, when set, registers an enforced equality matcher on this
	// label with the resolved tenant id, isolating the tenant's series.
	EnforceLabel string `mapstructure:"enforce_label"`
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

	return &Processor{Base: processor.Base{}, cfg: cfg}
}

// ProcessQuery resolves the tenant and, if configured, registers the isolation
// matcher.
func (p *Processor) ProcessQuery(_ context.Context, query *qdata.Query) error {
	tenantID := qdata.TenantID(query)
	if tenantID == "" {
		tenantID = headerValue(query, p.cfg.Header)
	}

	if tenantID == "" {
		tenantID = p.cfg.Default
	}

	if tenantID == "" {
		if p.cfg.Required {
			return qerror.New(qerror.CodeUnauthenticated, "tenant: missing %s header", p.cfg.Header)
		}

		return nil
	}

	qdata.SetTenantID(query, tenantID)

	if p.cfg.EnforceLabel != "" {
		query.EnforcedMatchers = append(query.EnforcedMatchers, &qdata.LabelMatcher{
			Name:  p.cfg.EnforceLabel,
			Op:    qdata.MatchEqual,
			Value: tenantID,
		})
	}

	return nil
}

// headerValue does a case-insensitive lookup of the first value of a header.
func headerValue(query *qdata.Query, name string) string {
	for key, values := range query.GetHeader() {
		if strings.EqualFold(key, name) && len(values.GetValues()) > 0 {
			return values.GetValues()[0]
		}
	}

	return ""
}
