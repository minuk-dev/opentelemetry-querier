package pipeline

import (
	"context"
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
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

func TestHandle_RunsRequestForwardAndResultReverse(t *testing.T) {
	var trace []string
	a := &recordingProc{name: "a", trace: &trace}
	b := &recordingProc{name: "b", trace: &trace}
	pl := New("test",
		[]processor.Processor{a, b},
		&stubDispatcher{result: &qdata.Result{}},
	)

	if _, err := pl.Handle(context.Background(), &qdata.Query{}); err != nil {
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

func TestHandle_DefaultsResultSignalFromQuery(t *testing.T) {
	pl := New("test", nil, &stubDispatcher{result: &qdata.Result{}})
	res, err := pl.Handle(context.Background(), &qdata.Query{Signal: qdata.SignalMetrics})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if res.GetSignal() != qdata.SignalMetrics {
		t.Fatalf("signal = %v, want metrics", res.GetSignal())
	}
}
