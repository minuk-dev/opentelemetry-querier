// Package simpleauthzprocessor implements a simple per-subject authorization processor.
// It resolves a subject (the user/principal a query runs as) from a request
// header or the resolved tenant, then evaluates an ordered list of policy rules
// to either reject the query outright (PermissionDenied) or scope it down by
// registering enforced label matchers that downstream
// [queryrewrite](../queryrewriteprocessor) weaves into the expression. It is meant to run
// after the tenant processor (so from_tenant works) and before queryrewrite (so
// the injected matchers are enforced).
//
// The processor fails closed: when no rule matches, the default_effect applies,
// and that defaults to deny. An empty policy therefore denies every query.
package simpleauthzprocessor

import (
	"context"
	"slices"
	"strings"

	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// DefaultSubjectHeader is the conventional request header carrying the subject
// (user) id when subject_header is not configured.
const DefaultSubjectHeader = "X-Scope-User"

// MetadataSubject is the metadata key holding the resolved subject id, set for
// downstream processors and observability.
const MetadataSubject = "authz.subject"

// Policy effects. Any value other than these is rejected at construction.
const (
	EffectAllow = "allow"
	EffectDeny  = "deny"
)

// EnforceLabel is a matcher injected into an allowed query to scope what the
// subject can see. It mirrors queryrewrite's EnforceLabel so a rule can pin a
// static value or the resolved tenant id.
type EnforceLabel struct {
	// Name is the label to constrain.
	Name string `mapstructure:"name"`
	// Value is a static matcher value; ignored when FromTenant is true.
	Value string `mapstructure:"value"`
	// FromTenant uses the resolved tenant id as the matcher value.
	FromTenant bool `mapstructure:"from_tenant"`
}

// Rule is one entry in the ordered policy. The first rule whose Subjects match
// the resolved subject decides the outcome; a rule with no Subjects is a
// catch-all.
type Rule struct {
	// Subjects lists the subject ids this rule applies to. Empty matches any
	// subject (catch-all), letting a trailing rule express a per-policy default.
	Subjects []string `mapstructure:"subjects"`
	// Effect is "allow" or "deny"; empty defaults to "deny".
	Effect string `mapstructure:"effect"`
	// EnforceLabels are injected when the rule allows the query.
	EnforceLabels []EnforceLabel `mapstructure:"enforce_labels"`
}

// Config configures subject resolution and the authorization policy.
// GroupRule grants or denies requests by a verified group name.
type GroupRule struct {
	Groups        []string       `mapstructure:"groups"`
	Effect        string         `mapstructure:"effect"`
	EnforceLabels []EnforceLabel `mapstructure:"enforce_labels"`
}

type Config struct {
	GroupRules []GroupRule `mapstructure:"group_rules"`
	// SubjectHeader is the request header carrying the subject id.
	SubjectHeader string `mapstructure:"subject_header"`
	// FromTenant resolves the subject from the tenant id instead of the header.
	FromTenant bool `mapstructure:"from_tenant"`
	// DefaultSubject is used when neither the header nor the tenant resolves one.
	DefaultSubject string `mapstructure:"default_subject"`
	// Required rejects queries for which no subject resolves.
	Required bool `mapstructure:"required"`
	// DefaultEffect ("allow" or "deny") applies when no rule matches; empty
	// defaults to "deny" so the processor fails closed.
	DefaultEffect string `mapstructure:"default_effect"`
	// Rules is the ordered policy; the first matching rule wins.
	Rules []Rule `mapstructure:"rules"`
}

// Processor authorizes queries per subject.
type Processor struct {
	processor.Base

	cfg Config
}

// SecurityRequirements declares dependencies of subject resolution and allow rules.
func (p *Processor) SecurityRequirements() processor.SecurityRequirements {
	var requirements processor.SecurityRequirements

	requirements.NeedsTenant = p.cfg.FromTenant

	for _, rule := range p.cfg.Rules {
		if denies(rule.Effect) {
			continue
		}

		requirements.RegistersEnforcement = requirements.RegistersEnforcement || len(rule.EnforceLabels) > 0

		for _, label := range rule.EnforceLabels {
			requirements.NeedsTenant = requirements.NeedsTenant || label.FromTenant
		}
	}

	for _, rule := range p.cfg.GroupRules {
		if denies(rule.Effect) {
			continue
		}
		requirements.RegistersEnforcement = requirements.RegistersEnforcement || len(rule.EnforceLabels) > 0
		for _, label := range rule.EnforceLabels {
			requirements.NeedsTenant = requirements.NeedsTenant || label.FromTenant
		}
	}
	return requirements
}

// New builds the processor, applying defaults. Callers should Validate the
// config first (the factory does); New assumes a valid effect set.
func New(cfg Config) *Processor {
	if cfg.SubjectHeader == "" {
		cfg.SubjectHeader = DefaultSubjectHeader
	}

	if cfg.DefaultEffect == "" {
		cfg.DefaultEffect = EffectDeny
	}

	return &Processor{Base: processor.Base{}, cfg: cfg}
}

// ProcessQuery resolves the subject and applies the policy: it rejects a denied
// query with PermissionDenied, or registers the matched rule's enforced
// matchers so downstream queryrewrite scopes the query.
func (p *Processor) ProcessQuery(ctx context.Context, query *qdata.Query) error {
	subject := p.resolveSubject(ctx, query)

	if subject == "" && p.cfg.Required {
		return qerror.New(qerror.CodePermissionDenied,
			"simpleauthz: no subject resolved from %s", p.cfg.SubjectHeader)
	}

	if subject != "" {
		qdata.SetMetadata(query, MetadataSubject, subject)
	}

	rule, matched := p.match(subject)

	if len(p.cfg.GroupRules) != 0 {
		return p.processGroups(ctx, query, rule, matched)
	}

	effect := p.cfg.DefaultEffect
	if matched {
		effect = rule.Effect
	}

	if denies(effect) {
		return qerror.New(qerror.CodePermissionDenied,
			"simpleauthz: subject %q denied by policy", subject)
	}

	if matched {
		return enforce(query, rule.EnforceLabels)
	}

	return nil
}

func (p *Processor) processGroups(ctx context.Context, query *qdata.Query, subject Rule, matched bool) error {
	identity, _ := qdata.IdentityFromContext(ctx)
	if matched && denies(subject.Effect) {
		return qerror.New(qerror.CodePermissionDenied, "simpleauthz: subject denied by policy")
	}

	allowed := false
	var labels []EnforceLabel
	for _, rule := range p.cfg.GroupRules {
		if !slices.ContainsFunc(rule.Groups, func(group string) bool { return slices.Contains(identity.Groups, group) }) {
			continue
		}
		if denies(rule.Effect) {
			return qerror.New(qerror.CodePermissionDenied, "simpleauthz: group denied by policy")
		}
		allowed = true
		labels = append(labels, rule.EnforceLabels...)
	}

	if !allowed {
		return qerror.New(qerror.CodePermissionDenied, "simpleauthz: no verified group grant")
	}
	if matched {
		labels = append(labels, subject.EnforceLabels...)
	}
	return enforce(query, labels)
}

// resolveSubject reads the subject from the tenant id or the configured header,
// falling back to the static default.
func (p *Processor) resolveSubject(ctx context.Context, query *qdata.Query) string {
	if identity, ok := qdata.IdentityFromContext(ctx); ok {
		if p.cfg.FromTenant {
			return identity.Tenant
		}
		return identity.Principal
	}

	var subject string

	if p.cfg.FromTenant {
		subject = qdata.TenantID(query)
	} else {
		subject = headerValue(query, p.cfg.SubjectHeader)
	}

	if subject == "" {
		subject = p.cfg.DefaultSubject
	}

	return subject
}

// match returns the first rule whose Subjects include the subject, treating an
// empty Subjects list as a catch-all.
func (p *Processor) match(subject string) (Rule, bool) {
	for _, rule := range p.cfg.Rules {
		if len(rule.Subjects) == 0 || slices.Contains(rule.Subjects, subject) {
			return rule, true
		}
	}

	return Rule{Subjects: nil, Effect: "", EnforceLabels: nil}, false
}

// enforce registers the rule's scoping matchers on the query for queryrewrite to
// weave in. A missing required tenant must never widen an allow rule.
func enforce(query *qdata.Query, labels []EnforceLabel) error {
	for _, label := range labels {
		value := label.Value
		if label.FromTenant {
			value = qdata.TenantID(query)
			if value == "" {
				return qerror.New(qerror.CodePermissionDenied, "simpleauthz: unresolved tenant for %q", label.Name)
			}
		}

		query.EnforcedMatchers = append(query.EnforcedMatchers, &qdata.LabelMatcher{
			Name:  label.Name,
			Op:    qdata.MatchEqual,
			Value: value,
		})
	}

	return nil
}

// denies permits only an explicit allow; omitted or unrecognized effects deny.
func denies(effect string) bool { return effect != EffectAllow }

// headerValue does a case-insensitive lookup of the first value of a header.
func headerValue(query *qdata.Query, name string) string {
	for key, values := range query.GetHeader() {
		if strings.EqualFold(key, name) && len(values.GetValues()) > 0 {
			return values.GetValues()[0]
		}
	}

	return ""
}
