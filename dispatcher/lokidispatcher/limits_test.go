package lokidispatcher

import (
	"strings"
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

func TestResultCardinalityLimit(t *testing.T) {
	t.Parallel()
	backend := New(Config{MaxRecords: 1})
	body := `{"status":"success","data":{"resultType":"streams","result":[{"stream":{},"values":[["1","one"]]}]}}`
	result, err := backend.parseResponse([]byte(body))
	if err != nil || result == nil {
		t.Fatalf("boundary rejected: %v", err)
	}
	marker := `[["1","one"]]`
	expanded := marker[:len(marker)-1] + "," + marker[1:]
	tooLarge := strings.Replace(body, marker, expanded, 1)
	result, err = backend.parseResponse([]byte(tooLarge))
	if result != nil || err == nil || qerror.CodeOf(err) != qerror.CodeResourceExhausted {
		t.Fatalf("over-limit result = %v, error = %v", result, err)
	}
}
