// Package responsefilterprocessor implements the response-transformation processor. It
// runs on the way out and reshapes a qdata Result: dropping internal attributes,
// masking sensitive values, and (for cumulative counters returned without a rate
// function) attaching a feedback notification per the spec's side-channel
// guidance.
package responsefilterprocessor

import (
	"context"
	"slices"

	qdatav1 "github.com/minuk-dev/opentelemetry-querier/gen/qdata/v1"
	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// Config configures response reshaping.
type Config struct {
	// DropLabels are attribute keys removed from every returned series/record.
	DropLabels []string `mapstructure:"drop_labels"`
	// MaskLabels are attribute keys whose values are replaced with MaskWith.
	MaskLabels []string `mapstructure:"mask_labels"`
	// MaskWith is the replacement value for masked attributes.
	MaskWith string `mapstructure:"mask_with"`
	// WarnCounterWithoutRate emits a feedback notification when a raw cumulative
	// counter is returned (spec: warn that data reflects raw counts).
	WarnCounterWithoutRate bool `mapstructure:"warn_counter_without_rate"`
}

// typeStr is the component type name.
const typeStr = "responsefilter"

// Processor reshapes results.
type Processor struct {
	processor.Base

	cfg Config
}

// New builds the response-filter processor.
func New(cfg Config) *Processor {
	if cfg.MaskWith == "" {
		cfg.MaskWith = "***"
	}

	return &Processor{Base: processor.Base{}, cfg: cfg}
}

// ProcessResult applies drop/mask to every signal's attributes and emits
// feedback where configured. The switch is a type switch over the Result.data
// oneof rather than a chain of nil checks so a payload this processor does not
// know how to scrub fails closed instead of being returned unfiltered.
func (p *Processor) ProcessResult(_ context.Context, _ *qdata.Query, result *qdata.Result) error {
	switch data := result.GetData().(type) {
	case *qdatav1.Result_Metrics:
		p.processMetrics(result, data.Metrics)
	case *qdatav1.Result_Logs:
		for _, record := range data.Logs.GetRecords() {
			p.scrub(record.GetAttributes())
		}
	case *qdatav1.Result_Spans:
		for _, span := range data.Spans.GetSpans() {
			p.scrub(span.GetAttributes())
		}
	case *qdatav1.Result_Table:
		p.scrubTable(data.Table)
	case nil:
		// A payload-less Result (feedback only) carries no attributes to scrub.
	default:
		return qerror.New(qerror.CodeInternal,
			"responsefilterprocessor: unhandled result payload %T; refusing to return unfiltered data", data)
	}

	return nil
}

// processMetrics scrubs each series and warns about raw cumulative counters.
func (p *Processor) processMetrics(result *qdata.Result, metrics *qdata.Metrics) {
	for _, series := range metrics.GetSeries() {
		p.scrub(series.GetAttributes())

		if p.cfg.WarnCounterWithoutRate && series.GetType() == qdata.MetricCumulativeCounter {
			qdata.Warn(result, "counter_without_rate",
				"series '"+series.GetName()+"' is a raw cumulative counter; apply rate() for per-second values",
				typeStr)
		}
	}
}

// scrubTable applies drop/mask to every row of a relational cross-signal Table
// (issue #66). A cross-signal join copies each side's attributes into its rows,
// so without this the join launders labels the operator dropped or masked on the
// single-signal path. Dropped keys also leave the declared column schema, so the
// schema keeps describing the rows it actually has; a masked key keeps its
// column and only loses its value.
func (p *Processor) scrubTable(table *qdata.Table) {
	if table == nil {
		return
	}

	for _, row := range table.GetRows() {
		p.scrub(row.GetValues())
	}

	if len(p.cfg.DropLabels) == 0 {
		return
	}

	table.Columns = slices.DeleteFunc(table.GetColumns(), func(column string) bool {
		return slices.Contains(p.cfg.DropLabels, column)
	})
}

func (p *Processor) scrub(attrs *qdata.KeyValueList) {
	if attrs == nil {
		return
	}

	for _, key := range p.cfg.DropLabels {
		qdata.AttrDelete(attrs, key)
	}

	for _, key := range p.cfg.MaskLabels {
		if _, ok := qdata.AttrGet(attrs, key); ok {
			qdata.AttrPutString(attrs, key, p.cfg.MaskWith)
		}
	}
}
