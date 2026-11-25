package prometheusdispatcher

import (
	"strings"
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

func TestResultCardinalityLimit(t *testing.T) {
	t.Parallel()
	backend := New(Config{MaxSeries: 1, MaxSamples: 1})
	body := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1,"1"]]}]}}`
	result, err := backend.parseResponse([]byte(body))
	if err != nil || result == nil {
		t.Fatalf("boundary rejected: %v", err)
	}
	marker := `[[1,"1"]]`
	expanded := marker[:len(marker)-1] + "," + marker[1:]
	tooLarge := strings.Replace(body, marker, expanded, 1)
	result, err = backend.parseResponse([]byte(tooLarge))
	if result != nil || err == nil || qerror.CodeOf(err) != qerror.CodeResourceExhausted {
		t.Fatalf("over-limit result = %v, error = %v", result, err)
	}
}
