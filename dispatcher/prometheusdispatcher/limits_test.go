package prometheusdispatcher_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher/prometheusdispatcher"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

func TestResultCardinalityLimit(t *testing.T) {
	t.Parallel()

	var cfg prometheusdispatcher.Config

	cfg.MaxSeries, cfg.MaxSamples = 1, 1
	body := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1,"1"]]}]}}`

	cfg.Endpoint = newServer(t, http.StatusOK, body).URL

	result, err := prometheusdispatcher.New(cfg).Dispatch(t.Context(), metricQuery("up"))
	if err != nil || result == nil {
		t.Fatalf("boundary rejected: %v", err)
	}

	marker := `[[1,"1"]]`
	expanded := marker[:len(marker)-1] + "," + marker[1:]
	tooLarge := strings.Replace(body, marker, expanded, 1)

	cfg.Endpoint = newServer(t, http.StatusOK, tooLarge).URL

	result, err = prometheusdispatcher.New(cfg).Dispatch(t.Context(), metricQuery("up"))
	if result != nil || err == nil || qerror.CodeOf(err) != qerror.CodeResourceExhausted {
		t.Fatalf("over-limit result = %v, error = %v", result, err)
	}
}
