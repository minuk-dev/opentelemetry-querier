package lokidispatcher_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher/lokidispatcher"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

func TestResultCardinalityLimit(t *testing.T) {
	t.Parallel()

	var cfg lokidispatcher.Config

	cfg.MaxRecords = 1
	body := `{"status":"success","data":{"resultType":"streams","result":[{"stream":{},"values":[["1","one"]]}]}}`

	cfg.Endpoint = newServer(t, http.StatusOK, body).URL

	result, err := lokidispatcher.New(cfg).Dispatch(t.Context(), logQLQuery())
	if err != nil || result == nil {
		t.Fatalf("boundary rejected: %v", err)
	}

	marker := `[["1","one"]]`
	expanded := marker[:len(marker)-1] + "," + marker[1:]
	tooLarge := strings.Replace(body, marker, expanded, 1)

	cfg.Endpoint = newServer(t, http.StatusOK, tooLarge).URL

	result, err = lokidispatcher.New(cfg).Dispatch(t.Context(), logQLQuery())
	if result != nil || err == nil || qerror.CodeOf(err) != qerror.CodeResourceExhausted {
		t.Fatalf("over-limit result = %v, error = %v", result, err)
	}
}
