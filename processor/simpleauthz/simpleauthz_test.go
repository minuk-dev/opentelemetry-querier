package simpleauthz_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/processor/simpleauthz"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// baseConfig returns a fully-specified default config so tests can tweak
// individual fields without tripping exhaustruct on partial literals.
func baseConfig() simpleauthz.Config {
	return simpleauthz.Config{
		SubjectHeader:  simpleauthz.DefaultSubjectHeader,
		FromTenant:     false,
		DefaultSubject: "",
		Required:       false,
		DefaultEffect:  simpleauthz.EffectDeny,
		Rules:          nil,
	}
}

// queryWithSubject builds a query carrying the given subject in the default
// subject header.
func queryWithSubject(subject string) *qdata.Query {
	query := &qdata.Query{Expr: "up"}
	if subject != "" {
		query.Header = map[string]*qdata.HeaderValues{
			simpleauthz.DefaultSubjectHeader: {Values: []string{subject}},
		}
	}

	return query
}

func TestDefaultEffectDeniesUnmatchedSubject(t *testing.T) {
	t.Parallel()

	// Empty policy → fail closed: an authenticated subject with no matching rule
	// is denied by the default deny effect.
	proc := simpleauthz.New(baseConfig())

	err := proc.ProcessQuery(context.Background(), queryWithSubject("alice"))

	require.Error(t, err)
	assert.Equal(t, qerror.CodePermissionDenied, qerror.CodeOf(err))
}

func TestRuleDenyRejects(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.DefaultEffect = simpleauthz.EffectAllow
	cfg.Rules = []simpleauthz.Rule{
		{Subjects: []string{"blocked"}, Effect: simpleauthz.EffectDeny, EnforceLabels: nil},
	}
	proc := simpleauthz.New(cfg)

	err := proc.ProcessQuery(context.Background(), queryWithSubject("blocked"))

	require.Error(t, err)
	assert.Equal(t, qerror.CodePermissionDenied, qerror.CodeOf(err))
}

func TestAllowInjectsEnforcedMatchers(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.Rules = []simpleauthz.Rule{
		{
			Subjects:      []string{"alice"},
			Effect:        simpleauthz.EffectAllow,
			EnforceLabels: []simpleauthz.EnforceLabel{{Name: "namespace", Value: "team-a", FromTenant: false}},
		},
	}
	proc := simpleauthz.New(cfg)

	query := queryWithSubject("alice")
	require.NoError(t, proc.ProcessQuery(context.Background(), query))

	require.Len(t, query.GetEnforcedMatchers(), 1)
	matcher := query.GetEnforcedMatchers()[0]
	assert.Equal(t, "namespace", matcher.GetName())
	assert.Equal(t, qdata.MatchEqual, matcher.GetOp())
	assert.Equal(t, "team-a", matcher.GetValue())
	assert.Equal(t, "alice", qdata.Metadata(query, simpleauthz.MetadataSubject))
}

func TestCatchAllRuleMatchesAnySubject(t *testing.T) {
	t.Parallel()

	// A rule with no Subjects is a catch-all — here it allows everyone.
	cfg := baseConfig()
	cfg.Rules = []simpleauthz.Rule{
		{Subjects: nil, Effect: simpleauthz.EffectAllow, EnforceLabels: nil},
	}
	proc := simpleauthz.New(cfg)

	require.NoError(t, proc.ProcessQuery(context.Background(), queryWithSubject("anyone")))
}

func TestFirstMatchingRuleWins(t *testing.T) {
	t.Parallel()

	// alice hits her explicit allow rule before the catch-all deny.
	cfg := baseConfig()
	cfg.Rules = []simpleauthz.Rule{
		{Subjects: []string{"alice"}, Effect: simpleauthz.EffectAllow, EnforceLabels: nil},
		{Subjects: nil, Effect: simpleauthz.EffectDeny, EnforceLabels: nil},
	}
	proc := simpleauthz.New(cfg)

	require.NoError(t, proc.ProcessQuery(context.Background(), queryWithSubject("alice")))

	err := proc.ProcessQuery(context.Background(), queryWithSubject("mallory"))
	require.Error(t, err)
	assert.Equal(t, qerror.CodePermissionDenied, qerror.CodeOf(err))
}

func TestRequiredRejectsMissingSubject(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.Required = true
	cfg.DefaultEffect = simpleauthz.EffectAllow
	proc := simpleauthz.New(cfg)

	err := proc.ProcessQuery(context.Background(), &qdata.Query{Expr: "up"})

	require.Error(t, err)
	assert.Equal(t, qerror.CodePermissionDenied, qerror.CodeOf(err))
}

func TestFromTenantResolvesSubjectAndValue(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.FromTenant = true
	cfg.Rules = []simpleauthz.Rule{
		{
			Subjects:      []string{"acme"},
			Effect:        simpleauthz.EffectAllow,
			EnforceLabels: []simpleauthz.EnforceLabel{{Name: "tenant_id", Value: "", FromTenant: true}},
		},
	}
	proc := simpleauthz.New(cfg)

	query := &qdata.Query{Expr: "up"}
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
	cfg.Rules = []simpleauthz.Rule{
		{
			Subjects:      []string{"alice"},
			Effect:        simpleauthz.EffectAllow,
			EnforceLabels: []simpleauthz.EnforceLabel{{Name: "namespace", Value: "", FromTenant: true}},
		},
	}
	proc := simpleauthz.New(cfg)

	query := queryWithSubject("alice")
	require.NoError(t, proc.ProcessQuery(context.Background(), query))
	assert.Empty(t, query.GetEnforcedMatchers())
}

func TestValidateRejectsUnknownEffect(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.DefaultEffect = "permit"
	require.Error(t, simpleauthz.Validate(cfg))

	cfg = baseConfig()
	cfg.Rules = []simpleauthz.Rule{{Subjects: nil, Effect: "block", EnforceLabels: nil}}
	require.Error(t, simpleauthz.Validate(cfg))
}

func TestValidateRejectsNamelessEnforceLabel(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.Rules = []simpleauthz.Rule{
		{
			Subjects:      nil,
			Effect:        simpleauthz.EffectAllow,
			EnforceLabels: []simpleauthz.EnforceLabel{{Name: "", Value: "x", FromTenant: false}},
		},
	}
	require.Error(t, simpleauthz.Validate(cfg))
}

func TestValidateAcceptsWellFormedPolicy(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.Rules = []simpleauthz.Rule{
		{
			Subjects:      []string{"alice"},
			Effect:        simpleauthz.EffectAllow,
			EnforceLabels: []simpleauthz.EnforceLabel{{Name: "ns", Value: "a", FromTenant: false}},
		},
		{Subjects: nil, Effect: simpleauthz.EffectDeny, EnforceLabels: nil},
	}
	require.NoError(t, simpleauthz.Validate(cfg))
}
