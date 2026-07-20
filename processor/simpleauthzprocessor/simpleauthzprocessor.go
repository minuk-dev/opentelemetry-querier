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
	// Effect is "allow" or "deny"; empty defaults to "allow".
	Effect string `mapstructure:"effect"`
	// EnforceLabels are injected when the rule allows the query.
	EnforceLabels []EnforceLabel `mapstructure:"enforce_labels"`
}

// Config configures subject resolution and the authorization policy.
type Config struct {
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
func (p *Processor) ProcessQuery(_ context.Context, query *qdata.Query) error {
	subject := p.resolveSubject(query)

	if subject == "" && p.cfg.Required {
		return qerror.New(qerror.CodePermissionDenied,
			"simpleauthz: no subject resolved from %s", p.cfg.SubjectHeader)
	}

	if subject != "" {
		qdata.SetMetadata(query, MetadataSubject, subject)
	}

	rule, matched := p.match(subject)

	effect := p.cfg.DefaultEffect
	if matched {
		effect = rule.Effect
	}

	if denies(effect) {
		return qerror.New(qerror.CodePermissionDenied,
			"simpleauthz: subject %q denied by policy", subject)
	}

	if matched {
		enforce(query, rule.EnforceLabels)
	}

	return nil
}

// resolveSubject reads the subject from the tenant id or the configured header,
// falling back to the static default.
func (p *Processor) resolveSubject(query *qdata.Query) string {
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
// weave in. Matchers with an empty resolved value are skipped rather than
// enforcing an empty string (which would over-restrict the query).
func enforce(query *qdata.Query, labels []EnforceLabel) {
	for _, label := range labels {
		value := label.Value
		if label.FromTenant {
			value = qdata.TenantID(query)
		}

		if value == "" {
			continue
		}

		query.EnforcedMatchers = append(query.EnforcedMatchers, &qdata.LabelMatcher{
			Name:  label.Name,
			Op:    qdata.MatchEqual,
			Value: value,
		})
	}
}

// denies reports whether an effect string rejects the query. Only "deny" denies;
// "allow" and the empty default allow. Validate rejects any other value, so an
// unrecognized effect never silently allows at runtime.
func denies(effect string) bool { return effect == EffectDeny }

// headerValue does a case-insensitive lookup of the first value of a header.
func headerValue(query *qdata.Query, name string) string {
	for key, values := range query.GetHeader() {
		if strings.EqualFold(key, name) && len(values.GetValues()) > 0 {
			return values.GetValues()[0]
		}
	}

	return ""
}
