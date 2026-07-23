package simpleauthzprocessor_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/processor/simpleauthzprocessor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// baseConfig returns a fully-specified default config so tests can tweak
// individual fields without tripping exhaustruct on partial literals.
func baseConfig() simpleauthzprocessor.Config {
	return simpleauthzprocessor.Config{
		SubjectHeader:  simpleauthzprocessor.DefaultSubjectHeader,
		FromTenant:     false,
		DefaultSubject: "",
		Required:       false,
		DefaultEffect:  simpleauthzprocessor.EffectDeny,
		Rules:          nil,
	}
}

// queryWithSubject builds a query carrying the given subject in the default
// subject header.
func queryWithSubject(subject string) *qdata.Query {
	query := &qdata.Query{}
	if subject != "" {
		query.Header = map[string]*qdata.HeaderValues{
			simpleauthzprocessor.DefaultSubjectHeader: {Values: []string{subject}},
		}
	}

	return query
}

func TestDefaultEffectDeniesUnmatchedSubject(t *testing.T) {
	t.Parallel()

	// Empty policy → fail closed: an authenticated subject with no matching rule
	// is denied by the default deny effect.
	proc := simpleauthzprocessor.New(baseConfig())

	err := proc.ProcessQuery(context.Background(), queryWithSubject("alice"))

	require.Error(t, err)
	assert.Equal(t, qerror.CodePermissionDenied, qerror.CodeOf(err))
}

func TestRuleDenyRejects(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.DefaultEffect = simpleauthzprocessor.EffectAllow
	cfg.Rules = []simpleauthzprocessor.Rule{
		{Subjects: []string{"blocked"}, Effect: simpleauthzprocessor.EffectDeny, EnforceLabels: nil},
	}
	proc := simpleauthzprocessor.New(cfg)

	err := proc.ProcessQuery(context.Background(), queryWithSubject("blocked"))

	require.Error(t, err)
	assert.Equal(t, qerror.CodePermissionDenied, qerror.CodeOf(err))
}

func TestAllowInjectsEnforcedMatchers(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.Rules = []simpleauthzprocessor.Rule{
		{
			Subjects:      []string{"alice"},
			Effect:        simpleauthzprocessor.EffectAllow,
			EnforceLabels: []simpleauthzprocessor.EnforceLabel{{Name: "namespace", Value: "team-a", FromTenant: false}},
		},
	}
	proc := simpleauthzprocessor.New(cfg)

	query := queryWithSubject("alice")
	require.NoError(t, proc.ProcessQuery(context.Background(), query))

	require.Len(t, query.GetEnforcedMatchers(), 1)
	matcher := query.GetEnforcedMatchers()[0]
	assert.Equal(t, "namespace", matcher.GetName())
	assert.Equal(t, qdata.MatchEqual, matcher.GetOp())
	assert.Equal(t, "team-a", matcher.GetValue())
	assert.Equal(t, "alice", qdata.Metadata(query, simpleauthzprocessor.MetadataSubject))
}

func TestCatchAllRuleMatchesAnySubject(t *testing.T) {
	t.Parallel()

	// A rule with no Subjects is a catch-all — here it allows everyone.
	cfg := baseConfig()
	cfg.Rules = []simpleauthzprocessor.Rule{
		{Subjects: nil, Effect: simpleauthzprocessor.EffectAllow, EnforceLabels: nil},
	}
	proc := simpleauthzprocessor.New(cfg)

	require.NoError(t, proc.ProcessQuery(context.Background(), queryWithSubject("anyone")))
}

func TestFirstMatchingRuleWins(t *testing.T) {
	t.Parallel()

	// alice hits her explicit allow rule before the catch-all deny.
	cfg := baseConfig()
	cfg.Rules = []simpleauthzprocessor.Rule{
		{Subjects: []string{"alice"}, Effect: simpleauthzprocessor.EffectAllow, EnforceLabels: nil},
		{Subjects: nil, Effect: simpleauthzprocessor.EffectDeny, EnforceLabels: nil},
	}
	proc := simpleauthzprocessor.New(cfg)

	require.NoError(t, proc.ProcessQuery(context.Background(), queryWithSubject("alice")))

	err := proc.ProcessQuery(context.Background(), queryWithSubject("mallory"))
	require.Error(t, err)
	assert.Equal(t, qerror.CodePermissionDenied, qerror.CodeOf(err))
}

func TestRequiredRejectsMissingSubject(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.Required = true
	cfg.DefaultEffect = simpleauthzprocessor.EffectAllow
	proc := simpleauthzprocessor.New(cfg)

	err := proc.ProcessQuery(context.Background(), &qdata.Query{})

	require.Error(t, err)
	assert.Equal(t, qerror.CodePermissionDenied, qerror.CodeOf(err))
}

func TestFromTenantResolvesSubjectAndValue(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.FromTenant = true
	cfg.Rules = []simpleauthzprocessor.Rule{
		{
			Subjects:      []string{"acme"},
			Effect:        simpleauthzprocessor.EffectAllow,
			EnforceLabels: []simpleauthzprocessor.EnforceLabel{{Name: "tenant_id", Value: "", FromTenant: true}},
		},
	}
	proc := simpleauthzprocessor.New(cfg)

	query := &qdata.Query{}
	qdata.SetTenantID(query, "acme")

	require.NoError(t, proc.ProcessQuery(context.Background(), query))
	require.Len(t, query.GetEnforcedMatchers(), 1)
	assert.Equal(t, "acme", query.GetEnforcedMatchers()[0].GetValue())
}

func TestEmptyEnforceValueSkipped(t *testing.T) {
	t.Parallel()

	// from_tenant with no tenant resolved yields an empty value; the matcher is
	// skipped rather than enforcing namespace="".
	cfg := baseConfig()
	cfg.Rules = []simpleauthzprocessor.Rule{
		{
			Subjects:      []string{"alice"},
			Effect:        simpleauthzprocessor.EffectAllow,
			EnforceLabels: []simpleauthzprocessor.EnforceLabel{{Name: "namespace", Value: "", FromTenant: true}},
		},
	}
	proc := simpleauthzprocessor.New(cfg)

	query := queryWithSubject("alice")
	require.NoError(t, proc.ProcessQuery(context.Background(), query))
	assert.Empty(t, query.GetEnforcedMatchers())
}

func TestValidateRejectsUnknownEffect(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.DefaultEffect = "permit"
	require.Error(t, simpleauthzprocessor.Validate(cfg))

	cfg = baseConfig()
	cfg.Rules = []simpleauthzprocessor.Rule{{Subjects: nil, Effect: "block", EnforceLabels: nil}}
	require.Error(t, simpleauthzprocessor.Validate(cfg))
}

func TestValidateRejectsNamelessEnforceLabel(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.Rules = []simpleauthzprocessor.Rule{
		{
			Subjects:      nil,
			Effect:        simpleauthzprocessor.EffectAllow,
			EnforceLabels: []simpleauthzprocessor.EnforceLabel{{Name: "", Value: "x", FromTenant: false}},
		},
	}
	require.Error(t, simpleauthzprocessor.Validate(cfg))
}

func TestValidateAcceptsWellFormedPolicy(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.Rules = []simpleauthzprocessor.Rule{
		{
			Subjects:      []string{"alice"},
			Effect:        simpleauthzprocessor.EffectAllow,
			EnforceLabels: []simpleauthzprocessor.EnforceLabel{{Name: "ns", Value: "a", FromTenant: false}},
		},
		{Subjects: nil, Effect: simpleauthzprocessor.EffectDeny, EnforceLabels: nil},
	}
	require.NoError(t, simpleauthzprocessor.Validate(cfg))
}
