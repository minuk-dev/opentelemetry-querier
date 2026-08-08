package qdata_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

func TestValidIdentifier(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		// Accepted: the identifiers real PromQL/LogQL uses.
		"up":                   true,
		"job":                  true,
		"__name__":             true,
		"_leading_underscore":  true,
		"http_requests_total":  true,
		"a1":                   true,
		"MixedCase":            true,
		"trailing_digits_9000": true,

		// Rejected: nothing can escape its construct (issue #64).
		"":                            false,
		"1leading_digit":              false,
		"job name":                    false,
		"http.status_code":            false,
		"job-name":                    false,
		`zz="1"} or secret_metric{x`:  false,
		`a) or secret_metric # `:      false,
		"abs(up) or secret_metric":    false,
		"job,other":                   false,
		"job\nother":                  false,
		`quoted"`:                     false,
		"service.name":                false,
		"k8s.pod.name":                false,
		"job{":                        false,
		"job}":                        false,
		"__name__=~\".+\"":            false,
		"label[5m]":                   false,
		"sum by(x) (up) or secret":    false,
		"été":                         false,
		"job\t":                       false,
		"job or {x=\"1\"}":            false,
		"a) group_left() secret_metr": false,
	}

	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, want, qdata.ValidIdentifier(name))
		})
	}
}
