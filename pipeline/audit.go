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
	identity qdata.Identity
	verified bool
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
	code := qerror.CodeOf(err)
	if code == qerror.CodePermissionDenied || code == qerror.CodeUnauthenticated {
		d.decision = "deny"
		d.reason = reason
	}
}

func (p *Pipeline) audit(ctx context.Context, decision auditDecision, err error) {
	outcome := "success"
	if err != nil {
		outcome = "error"
	}

	args := []slog.Attr{
		slog.String("event", "query.authorization"),
		slog.Any("request_id", ctx.Value(auditIDKey{})),
		slog.String("pipeline", p.Name),
		slog.String("decision", decision.decision),
		slog.String("reason", decision.reason),
		slog.String("outcome", outcome),
	}
	if err != nil {
		args = append(args, slog.Int("error_code", int(qerror.CodeOf(err))))
	}

	if decision.verified {
		args = append(args,
			slog.String("authority", decision.identity.Authority),
			slog.String("principal", decision.identity.Principal),
			slog.String("tenant", decision.identity.Tenant),
		)
	}

	p.AuditLogger.LogAttrs(ctx, slog.LevelInfo, "query authorization", args...)
}
