// Package responsefilterprocessor implements the response-transformation processor. It
// runs on the way out and reshapes a qdata Result: dropping internal attributes,
// masking sensitive values, and (for cumulative counters returned without a rate
// function) attaching a feedback notification per the spec's side-channel
// guidance.
package responsefilterprocessor

import (
	"context"

	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
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
// feedback where configured.
func (p *Processor) ProcessResult(_ context.Context, _ *qdata.Query, result *qdata.Result) error {
	switch {
	case result.GetMetrics() != nil:
		for _, series := range result.GetMetrics().GetSeries() {
			p.scrub(series.GetAttributes())

			if p.cfg.WarnCounterWithoutRate && series.GetType() == qdata.MetricCumulativeCounter {
				qdata.Warn(result, "counter_without_rate",
					"series '"+series.GetName()+"' is a raw cumulative counter; apply rate() for per-second values",
					typeStr)
			}
		}
	case result.GetLogs() != nil:
		for _, record := range result.GetLogs().GetRecords() {
			p.scrub(record.GetAttributes())
		}
	case result.GetSpans() != nil:
		for _, span := range result.GetSpans().GetSpans() {
			p.scrub(span.GetAttributes())
		}
	}

	return nil
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
