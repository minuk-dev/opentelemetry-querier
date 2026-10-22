package pipeline_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/processor/queryrewriteprocessor"
	"github.com/minuk-dev/opentelemetry-querier/processor/simpleauthzprocessor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

func TestInvalidSecurityOrderNeverStartsOrDispatches(t *testing.T) {
	t.Parallel()

	chain := defaultChain()

	cases := map[string][]processor.Processor{
		"missing rewrite":       {chain[0], chain[1], chain[2]},
		"rewrite before authz":  {chain[0], chain[1], chain[3], chain[2]},
		"limiter before tenant": {chain[1], chain[0], chain[2], chain[3]},
		"rewrite before tenant": {chain[3], chain[0], chain[1], chain[2]},
		"no tenant resolver":    {chain[1], chain[2], chain[3]},
		"isolation only":        {chain[0]},
	}

	for name, procs := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// A nil dispatcher makes any accidental startup or dispatch fail the test.
			pipe := pipeline.New("metrics/security", procs, nil)
			require.ErrorContains(t, pipe.Start(t.Context(), nil), "invalid security processor order")
			_, err := pipe.Handle(t.Context(), &qdata.Query{})
			require.ErrorContains(t, err, "invalid security processor order")
		})
	}
}

func TestAuthorizationTenantDependencies(t *testing.T) {
	t.Parallel()

	cases := map[string]simpleauthzprocessor.Config{
		"subject": {
			SubjectHeader: "", FromTenant: true, DefaultSubject: "", Required: false,
			DefaultEffect: "allow", Rules: nil,
		},
		"allow matcher": {
			SubjectHeader: "", FromTenant: false, DefaultSubject: "", Required: false, DefaultEffect: "deny",
			Rules: []simpleauthzprocessor.Rule{{Subjects: nil, Effect: "allow",
				EnforceLabels: []simpleauthzprocessor.EnforceLabel{{Name: "tenant_id", Value: "", FromTenant: true}}}},
		},
	}

	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			authz := simpleauthzprocessor.New(cfg)
			rewrite := queryrewriteprocessor.New(queryrewriteprocessor.Config{EnforceLabels: nil})
			require.ErrorContains(t, processor.ValidateOrder([]processor.Processor{authz, rewrite}), "tenant resolver")
			require.NoError(t, processor.ValidateOrder([]processor.Processor{plainTenant(), authz, rewrite}))
		})
	}
}
