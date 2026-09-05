package responsefilterprocessor_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	qdatav1 "github.com/minuk-dev/opentelemetry-querier/gen/qdata/v1"
	"github.com/minuk-dev/opentelemetry-querier/processor/responsefilterprocessor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

const (
	// dropKey is the attribute the tests configure as dropped, maskKey the one
	// configured as masked, and keepKey a bystander that must survive both.
	dropKey = "internal_id"
	maskKey = "user_email"
	keepKey = "job"

	maskValue = "***"
)

// scrubber builds a processor that drops dropKey and masks maskKey.
func scrubber() *responsefilterprocessor.Processor {
	return responsefilterprocessor.New(responsefilterprocessor.Config{
		DropLabels:             []string{dropKey},
		MaskLabels:             []string{maskKey},
		MaskWith:               "",
		WarnCounterWithoutRate: false,
	})
}

// sensitiveAttrs builds the attribute set every arm is expected to scrub.
func sensitiveAttrs() *qdata.KeyValueList {
	return qdata.NewAttrs(
		dropKey, qdata.Str("secret-123"),
		maskKey, qdata.Str("victim@example.com"),
		keepKey, qdata.Str("api"),
	)
}

// assertScrubbed asserts the drop/mask/keep outcome on one attribute set.
func assertScrubbed(t *testing.T, attrs *qdata.KeyValueList) {
	t.Helper()

	_, dropped := qdata.AttrGet(attrs, dropKey)
	assert.False(t, dropped, "the dropped attribute is gone")

	masked, ok := qdata.AttrGet(attrs, maskKey)
	require.True(t, ok, "the masked attribute keeps its key")
	assert.Equal(t, maskValue, masked.GetStringValue(), "the masked value is replaced")

	kept, ok := qdata.AttrGet(attrs, keepKey)
	require.True(t, ok, "other attributes survive")
	assert.Equal(t, "api", kept.GetStringValue())
}

// TestProcessResultTable pins the cross-signal path (issue #66): a relational
// Table is the payload of a join, and its rows carry the very attributes the
// single-signal arms scrub, so drop/mask must apply there too.
func TestProcessResultTable(t *testing.T) {
	t.Parallel()

	table := qdata.NewTable([]string{dropKey, maskKey, keepKey}, &qdata.Row{Values: sensitiveAttrs()})
	result := qdata.TableResult(table)

	require.NoError(t, scrubber().ProcessResult(context.Background(), &qdata.Query{}, result))

	assertScrubbed(t, table.GetRows()[0].GetValues())

	assert.Equal(t, []string{maskKey, keepKey}, table.GetColumns(),
		"a dropped attribute also leaves the declared schema; a masked one keeps its column")
	require.NoError(t, qdata.ValidateTable(table), "the scrubbed table stays well-formed")

	// The wire rendering is what the client actually sees.
	wire := qdata.RenderTable(table)
	assert.Equal(t, []string{maskKey, keepKey}, wire.Columns)
	assert.Equal(t, [][]any{{maskValue, "api"}}, wire.Rows)
}

// TestProcessResultEmptyTable pins that a Table payload without rows or schema
// is handled rather than panicking.
func TestProcessResultEmptyTable(t *testing.T) {
	t.Parallel()

	require.NoError(t, scrubber().ProcessResult(context.Background(), &qdata.Query{}, qdata.TableResult(nil)))
}

// TestProcessResultSignals covers the single-signal arms, so the switch rewrite
// keeps its existing behavior.
func TestProcessResultSignals(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		result *qdata.Result
		attrs  func(*qdata.Result) *qdata.KeyValueList
	}{
		"metrics": {
			result: &qdata.Result{Data: &qdatav1.Result_Metrics{Metrics: &qdata.Metrics{
				Series: []*qdata.MetricSeries{{Attributes: sensitiveAttrs()}},
			}}},
			attrs: func(r *qdata.Result) *qdata.KeyValueList {
				return r.GetMetrics().GetSeries()[0].GetAttributes()
			},
		},
		"logs": {
			result: &qdata.Result{Data: &qdatav1.Result_Logs{Logs: &qdata.Logs{
				Records: []*qdata.LogRecord{{Attributes: sensitiveAttrs()}},
			}}},
			attrs: func(r *qdata.Result) *qdata.KeyValueList {
				return r.GetLogs().GetRecords()[0].GetAttributes()
			},
		},
		"spans": {
			result: &qdata.Result{Data: &qdatav1.Result_Spans{Spans: &qdata.Spans{
				Spans: []*qdata.Span{{Attributes: sensitiveAttrs()}},
			}}},
			attrs: func(r *qdata.Result) *qdata.KeyValueList {
				return r.GetSpans().GetSpans()[0].GetAttributes()
			},
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			require.NoError(t, scrubber().ProcessResult(context.Background(), &qdata.Query{}, testCase.result))
			assertScrubbed(t, testCase.attrs(testCase.result))
		})
	}
}

// TestProcessResultCounterWarning pins the counter-without-rate feedback, which
// rides along the metrics arm.
func TestProcessResultCounterWarning(t *testing.T) {
	t.Parallel()

	proc := responsefilterprocessor.New(responsefilterprocessor.Config{
		DropLabels:             nil,
		MaskLabels:             nil,
		MaskWith:               "",
		WarnCounterWithoutRate: true,
	})

	result := &qdata.Result{Data: &qdatav1.Result_Metrics{Metrics: &qdata.Metrics{
		Series: []*qdata.MetricSeries{{Name: "http_requests_total", Type: qdata.MetricCumulativeCounter}},
	}}}

	require.NoError(t, proc.ProcessResult(context.Background(), &qdata.Query{}, result))

	notifications := result.GetFeedback().GetNotifications()

	require.Len(t, notifications, 1)
	assert.Equal(t, "counter_without_rate", notifications[0].GetCode())
	assert.Contains(t, notifications[0].GetMessage(), "http_requests_total")
}

// TestProcessResultEmptyPayload pins that a payload-less Result (feedback only)
// is not treated as an unhandled payload: it carries nothing to scrub. The
// switch's fail-closed default arm is unreachable from a test because the
// Result.data oneof interface is sealed by the generated package — it exists to
// catch a future proto arm added without a matching arm here, which is exactly
// how the Table payload slipped through (issue #66).
func TestProcessResultEmptyPayload(t *testing.T) {
	t.Parallel()

	require.NoError(t, scrubber().ProcessResult(context.Background(), &qdata.Query{}, &qdata.Result{}))
}
