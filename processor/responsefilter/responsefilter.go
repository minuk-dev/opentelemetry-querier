// Package responsefilter implements the response-transformation processor. It
// runs on the way out and reshapes a qdata Result: dropping internal attributes,
// masking sensitive values, and (for cumulative counters returned without a rate
// function) attaching a feedback notification per the spec's side-channel
// guidance.
package responsefilter

import (
	"context"

	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// Config configures response reshaping.
type Config struct {
	// DropLabels are attribute keys removed from every returned series/record.
	DropLabels []string `yaml:"drop_labels"`
	// MaskLabels are attribute keys whose values are replaced with MaskWith.
	MaskLabels []string `yaml:"mask_labels"`
	// MaskWith is the replacement value for masked attributes.
	MaskWith string `yaml:"mask_with"`
	// WarnCounterWithoutRate emits a feedback notification when a raw cumulative
	// counter is returned (spec: warn that data reflects raw counts).
	WarnCounterWithoutRate bool `yaml:"warn_counter_without_rate"`
}

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
	return &Processor{cfg: cfg}
}

func (p *Processor) Name() string { return "responsefilter" }

// ProcessResult applies drop/mask to every signal's attributes and emits
// feedback where configured.
func (p *Processor) ProcessResult(_ context.Context, _ *qdata.Query, r *qdata.Result) error {
	switch {
	case r.GetMetrics() != nil:
		for _, s := range r.GetMetrics().GetSeries() {
			p.scrub(s.GetAttributes())
			if p.cfg.WarnCounterWithoutRate && s.GetType() == qdata.MetricCumulativeCounter {
				qdata.Warn(r, "counter_without_rate",
					"series '"+s.GetName()+"' is a raw cumulative counter; apply rate() for per-second values",
					p.Name())
			}
		}
	case r.GetLogs() != nil:
		for _, rec := range r.GetLogs().GetRecords() {
			p.scrub(rec.GetAttributes())
		}
	case r.GetSpans() != nil:
		for _, sp := range r.GetSpans().GetSpans() {
			p.scrub(sp.GetAttributes())
		}
	}
	return nil
}

func (p *Processor) scrub(attrs *qdata.KeyValueList) {
	if attrs == nil {
		return
	}
	for _, k := range p.cfg.DropLabels {
		qdata.AttrDelete(attrs, k)
	}
	for _, k := range p.cfg.MaskLabels {
		if _, ok := qdata.AttrGet(attrs, k); ok {
			qdata.AttrPutString(attrs, k, p.cfg.MaskWith)
		}
	}
}
