package pipeline

import (
	"context"
	"crypto/rand"
	"log/slog"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

type auditIDKey struct{}

type auditDecision struct {
	ctx      context.Context
	decision string
	reason   string
}

func withAuditRequestID(ctx context.Context) context.Context {
	if ctx.Value(auditIDKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, auditIDKey{}, rand.Text())
}

func (d *auditDecision) recordDenial(err error, reason string) {
	switch qerror.CodeOf(err) {
	case qerror.CodePermissionDenied, qerror.CodeUnauthenticated:
		d.decision = "deny"
		d.reason = reason
	}
}

func (p *Pipeline) audit(d auditDecision, err error) {
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	args := []slog.Attr{
		slog.String("event", "query.authorization"),
		slog.Any("request_id", d.ctx.Value(auditIDKey{})),
		slog.String("pipeline", p.Name),
		slog.String("decision", d.decision),
		slog.String("reason", d.reason),
		slog.String("outcome", outcome),
	}
	if err != nil {
		args = append(args, slog.Int("error_code", int(qerror.CodeOf(err))))
	}
	if identity, ok := qdata.IdentityFromContext(d.ctx); ok {
		args = append(args, slog.String("authority", identity.Authority), slog.String("principal", identity.Principal), slog.String("tenant", identity.Tenant))
	}
	p.AuditLogger.LogAttrs(d.ctx, slog.LevelInfo, "query authorization", args...)
}
