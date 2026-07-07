package pipeline_test

import (
	"context"
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

// recordingProc records the order in which its hooks run.
type recordingProc struct {
	processor.Base

	name  string
	trace *[]string
}

func (p *recordingProc) ProcessQuery(_ context.Context, _ *qdata.Query) error {
	*p.trace = append(*p.trace, "query:"+p.name)

	return nil
}

func (p *recordingProc) ProcessResult(_ context.Context, _ *qdata.Query, _ *qdata.Result) error {
	*p.trace = append(*p.trace, "result:"+p.name)

	return nil
}

type stubDispatcher struct {
	dispatcher.Base

	result *qdata.Result
}

func (d *stubDispatcher) Dispatch(_ context.Context, _ *qdata.Query) (*qdata.Result, error) {
	return d.result, nil
}

func TestHandleRunsRequestForwardAndResultReverse(t *testing.T) {
	t.Parallel()

	var trace []string

	procA := &recordingProc{Base: processor.Base{}, name: "a", trace: &trace}
	procB := &recordingProc{Base: processor.Base{}, name: "b", trace: &trace}
	pipe := pipeline.New("test",
		[]processor.Processor{procA, procB},
		&stubDispatcher{Base: dispatcher.Base{}, result: &qdata.Result{}},
	)

	_, err := pipe.Handle(context.Background(), &qdata.Query{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	want := []string{"query:a", "query:b", "result:b", "result:a"}
	if len(trace) != len(want) {
		t.Fatalf("trace = %v, want %v", trace, want)
	}

	for i := range want {
		if trace[i] != want[i] {
			t.Fatalf("trace = %v, want %v", trace, want)
		}
	}
}

func TestHandleDefaultsResultSignalFromQuery(t *testing.T) {
	t.Parallel()

	pipe := pipeline.New("test", nil, &stubDispatcher{Base: dispatcher.Base{}, result: &qdata.Result{}})

	result, err := pipe.Handle(context.Background(), &qdata.Query{Signal: qdata.SignalMetrics})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if result.GetSignal() != qdata.SignalMetrics {
		t.Fatalf("signal = %v, want metrics", result.GetSignal())
	}
}
