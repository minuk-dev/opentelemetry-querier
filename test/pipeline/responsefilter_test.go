package pipeline_test

import (
	"context"
	"net/http"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/stretchr/testify/assert"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	qdatav1 "github.com/minuk-dev/opentelemetry-querier/gen/qdata/v1"
	"github.com/minuk-dev/opentelemetry-querier/processor/responsefilterprocessor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// The labels the response filter is configured to scrub in these tests, and the
// replacement the masked one carries.
const (
	dropLabel = "__internal__"
	maskLabel = "user_email"
	maskValue = "***"
)

// TestResponseFilter adds the response-filter processor and asserts its effect
// on the response path end to end: dropped labels vanish from the returned
// series, masked labels are replaced, and a raw cumulative counter returned
// without rate() surfaces a warning in the Prometheus response envelope.
func TestResponseFilter(t *testing.T) {
	t.Parallel()

	t.Run("drops and masks labels on the way out", func(t *testing.T) {
		t.Parallel()

		filter := responsefilterprocessor.New(responsefilterprocessor.Config{
			DropLabels:             []string{dropLabel},
			MaskLabels:             []string{maskLabel},
			MaskWith:               maskValue,
			WarnCounterWithoutRate: false,
		})

		upstream := newUpstream(t)
		upstream.setBody(`{"status":"success","data":{"resultType":"vector","result":[{"metric":` +
			`{"__name__":"up","job":"api","__internal__":"secret","user_email":"a@b.com"},` +
			`"value":[1700000000,"1"]}]}}`)

		base := front(t, upstream, filter)

		code, body := getWith(t, base, nil)

		assert.Equal(t, http.StatusOK, code, "body: %s", body)
		assert.NotContains(t, body, dropLabel, "the dropped label is gone")
		assert.NotContains(t, body, "a@b.com", "the masked value is not leaked")
		assert.Contains(t, body, `"`+maskLabel+`":"`+maskValue+`"`, "the masked label carries the replacement value")
		assert.Contains(t, body, `"job":"api"`, "untouched labels survive")
	})

	t.Run("warns on a raw cumulative counter without rate()", func(t *testing.T) {
		t.Parallel()

		filter := responsefilterprocessor.New(responsefilterprocessor.Config{
			DropLabels:             nil,
			MaskLabels:             nil,
			MaskWith:               "",
			WarnCounterWithoutRate: true,
		})

		// The Prometheus wire format does not carry a metric type, so a real
		// dispatcher always yields UNKNOWN; a fake dispatcher lets us return the
		// cumulative counter the warning keys on while still exercising the
		// acceptor -> responsefilter -> feedback serialization path.
		base := frontWith(t, counterDispatcher{Base: dispatcher.Base{}}, filter)

		code, body := getWith(t, base, nil)

		assert.Equal(t, http.StatusOK, code, "body: %s", body)
		assert.Contains(t, body, `"warnings"`, "the feedback notification is serialized as a warning")
		assert.Contains(t, body, "rate()", "the warning tells the client to apply rate()")
		assert.Contains(t, body, "http_requests_total", "the warning names the offending series")
	})
}

// TestResponseFilterCrossSignalTable pins the cross-signal path (issue #66): a
// join returns a relational Table whose rows carry both sides' attributes, so
// the response filter must scrub them there too or the join launders the labels
// the operator dropped or masked on the single-signal path.
func TestResponseFilterCrossSignalTable(t *testing.T) {
	t.Parallel()

	filter := responsefilterprocessor.New(responsefilterprocessor.Config{
		DropLabels:             []string{dropLabel},
		MaskLabels:             []string{maskLabel},
		MaskWith:               maskValue,
		WarnCounterWithoutRate: false,
	})

	base := frontWith(t, tableDispatcher{Base: dispatcher.Base{}}, filter)

	code, body := getWith(t, base, nil)

	assert.Equal(t, http.StatusOK, code, "body: %s", body)
	assert.NotContains(t, body, dropLabel, "the dropped column is gone from the table")
	assert.NotContains(t, body, "a@b.com", "the masked value is not leaked")
	assert.Contains(t, body, `"`+maskValue+`"`, "the masked column carries the replacement value")
	assert.Contains(t, body, `"job"`, "untouched columns survive")
}

// TestResponseFilterNoWarningThroughRealDispatcher pins the limitation the fake
// dispatcher in TestResponseFilter works around: through the real acceptor ->
// dispatcher path the Prometheus wire format carries no metric type, so every
// series is UNKNOWN and the counter-without-rate check never fires — even with
// WarnCounterWithoutRate enabled. This exercises the real end-to-end path so a
// regression in metric-type handling would be caught here.
func TestResponseFilterNoWarningThroughRealDispatcher(t *testing.T) {
	t.Parallel()

	filter := responsefilterprocessor.New(responsefilterprocessor.Config{
		DropLabels:             nil,
		MaskLabels:             nil,
		MaskWith:               "",
		WarnCounterWithoutRate: true,
	})

	upstream := newUpstream(t)
	base := front(t, upstream, filter)

	code, body := getWith(t, base, nil)

	assert.Equal(t, http.StatusOK, code, "body: %s", body)
	assert.NotContains(t, body, "rate()", "an untyped series cannot trigger the counter warning")
}

// counterDispatcher is a fake dispatcher that returns a single raw cumulative
// counter series, standing in for storage on the response path.
type counterDispatcher struct {
	dispatcher.Base
}

func (counterDispatcher) Dispatch(_ context.Context, _ *qdata.Query) (*qdata.Result, error) {
	now := timestamppb.Now()

	return &qdata.Result{
		Signal: qdata.SignalMetrics,
		Data: &qdatav1.Result_Metrics{Metrics: &qdata.Metrics{Series: []*qdata.MetricSeries{{
			Name:       "http_requests_total",
			Type:       qdata.MetricCumulativeCounter,
			Attributes: &qdata.KeyValueList{},
			Points:     []*qdata.MetricPoint{{Start: now, End: now, Value: qdata.Double(42)}},
		}}}},
	}, nil
}

// tableDispatcher is a fake dispatcher that returns the relational Table a
// cross-signal join produces, standing in for the connector on the response path.
type tableDispatcher struct {
	dispatcher.Base
}

func (tableDispatcher) Dispatch(_ context.Context, _ *qdata.Query) (*qdata.Result, error) {
	row := qdata.NewRow(
		"job", qdata.Str("api"),
		dropLabel, qdata.Str("secret"),
		maskLabel, qdata.Str("a@b.com"),
	)

	return qdata.TableResult(qdata.NewTable([]string{"job", dropLabel, maskLabel}, row)), nil
}
