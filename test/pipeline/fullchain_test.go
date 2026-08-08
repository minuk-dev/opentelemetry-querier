package pipeline_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/processor/authratelimitprocessor"
	"github.com/minuk-dev/opentelemetry-querier/processor/queryrewriteprocessor"
	"github.com/minuk-dev/opentelemetry-querier/processor/responsefilterprocessor"
	"github.com/minuk-dev/opentelemetry-querier/processor/simpleauthzprocessor"
	"github.com/minuk-dev/opentelemetry-querier/processor/tenantprocessor"
)

// defaultChain builds the full default processor chain from config.yaml:
// authratelimit -> tenant -> simpleauthz -> queryrewrite -> responsefilter.
func defaultChain() []processor.Processor {
	return []processor.Processor{
		authratelimitprocessor.New(authratelimitprocessor.Config{
			RequireBearer:     true,
			Tokens:            []string{"dev-token"},
			RequestsPerSecond: 1000,
			Burst:             1000,
			PerTenant:         true,
		}),
		tenantprocessor.New(tenantprocessor.Config{
			Header:       tenantHeader,
			Default:      "anonymous",
			Required:     false,
			EnforceLabel: "tenant_id",
		}),
		simpleauthzprocessor.New(simpleauthzprocessor.Config{
			SubjectHeader:  "X-Scope-User",
			FromTenant:     false,
			DefaultSubject: "",
			Required:       false,
			DefaultEffect:  simpleauthzprocessor.EffectDeny,
			Rules: []simpleauthzprocessor.Rule{
				{Subjects: []string{"admin"}, Effect: simpleauthzprocessor.EffectAllow, EnforceLabels: nil},
				{
					Subjects: []string{"alice"},
					Effect:   simpleauthzprocessor.EffectAllow,
					EnforceLabels: []simpleauthzprocessor.EnforceLabel{
						{Name: "namespace", Value: "team-a", FromTenant: false},
					},
				},
			},
		}),
		queryrewriteprocessor.New(queryrewriteprocessor.Config{
			EnforceLabels: []queryrewriteprocessor.EnforceLabel{
				{Name: "tenant_id", Value: "", FromTenant: true},
			},
		}),
		responsefilterprocessor.New(responsefilterprocessor.Config{
			DropLabels:             []string{"__internal__"},
			MaskLabels:             []string{"user_email"},
			MaskWith:               "***",
			WarnCounterWithoutRate: true,
		}),
	}
}

// TestFullChain exercises the whole default pipeline at once, asserting that the
// per-processor effects validated in isolation compose: auth gates the request,
// the tenant is resolved and forwarded, the subject is authorized and scoped
// down, the enforced matchers land in the rendered upstream query, and the
// response is scrubbed on the way out.
func TestFullChain(t *testing.T) {
	t.Parallel()

	t.Run("authorized scoped request flows through every stage", func(t *testing.T) {
		t.Parallel()

		upstream := newUpstream(t)
		upstream.setBody(`{"status":"success","data":{"resultType":"vector","result":[{"metric":` +
			`{"__name__":"up","job":"api","__internal__":"secret","user_email":"a@b.com"},` +
			`"value":[1700000000,"1"]}]}}`)

		base := front(t, upstream, defaultChain()...)

		code, body := getWith(t, base, map[string]string{
			"Authorization": "Bearer dev-token",
			tenantHeader:    "acme",
			"X-Scope-User":  "alice",
		})

		assert.Equal(t, http.StatusOK, code, "body: %s", body)
		assert.Equal(t, "acme", upstream.gotTenant(), "the resolved tenant is forwarded upstream")
		// alice's scope-down (namespace=team-a) and the enforced tenant_id both
		// land in the rendered query.
		assert.Contains(t, upstream.gotQuery(), `namespace="team-a"`, "subject scope-down is enforced")
		assert.Contains(t, upstream.gotQuery(), `tenant_id="acme"`, "tenant isolation is enforced")
		// responsefilter scrubs the response on the way out.
		assert.NotContains(t, body, "__internal__", "the dropped label is gone")
		assert.NotContains(t, body, "a@b.com", "the masked value is not leaked")
		assert.Contains(t, body, `"user_email":"***"`, "the masked label carries the replacement")
	})

	t.Run("missing bearer is rejected before any stage runs", func(t *testing.T) {
		t.Parallel()

		upstream := newUpstream(t)
		base := front(t, upstream, defaultChain()...)

		code, body := getWith(t, base, map[string]string{tenantHeader: "acme", "X-Scope-User": "admin"})

		assert.Equal(t, http.StatusUnauthorized, code, "body: %s", body)
		assert.Empty(t, upstream.gotQuery(), "the gateway short-circuits before the dispatcher")
	})

	t.Run("unauthorized subject is denied by policy", func(t *testing.T) {
		t.Parallel()

		upstream := newUpstream(t)
		base := front(t, upstream, defaultChain()...)

		code, body := getWith(t, base, map[string]string{
			"Authorization": "Bearer dev-token",
			tenantHeader:    "acme",
			"X-Scope-User":  "stranger",
		})

		assert.Equal(t, http.StatusForbidden, code, "no rule matches; default_effect denies. body: %s", body)
		assert.Empty(t, upstream.gotQuery())
	})
}
