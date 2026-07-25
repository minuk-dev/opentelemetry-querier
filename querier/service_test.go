package querier_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/acceptor"
	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/connector"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/querier"
)

type emptyConfig struct{}

// fakeAcceptor is a no-op acceptor.
type fakeAcceptor struct{}

func (fakeAcceptor) Start(context.Context, component.Host) error { return nil }

func (fakeAcceptor) Shutdown(context.Context) error { return nil }

// fakeConnector is a no-op connector recording the targets it was wired with.
type fakeConnector struct {
	dispatcher.Base

	targets map[qdata.Signal]pipeline.Handler
}

func (fakeConnector) Dispatch(context.Context, *qdata.Query) (*qdata.Result, error) {
	return &qdata.Result{}, nil
}

func emptyDefault() component.Config { return &emptyConfig{} }

func acceptorFactory(typ string) acceptor.Factory {
	return acceptor.NewFactory(component.MustNewType(typ), emptyDefault,
		func(context.Context, component.Settings, component.Config, pipeline.Handler) (acceptor.Acceptor, error) {
			return fakeAcceptor{}, nil
		})
}

func dispatcherFactory(typ string) dispatcher.Factory {
	return dispatcher.NewFactory(component.MustNewType(typ), emptyDefault,
		func(context.Context, component.Settings, component.Config) (dispatcher.Dispatcher, error) {
			return fakeConnector{Base: dispatcher.Base{}, targets: nil}, nil
		})
}

// recordingConnFactory is a connector factory that captures the targets it is
// wired with into *captured.
func recordingConnFactory(captured *map[qdata.Signal]pipeline.Handler) connector.Factory {
	return connector.NewFactory(component.MustNewType("crosssignal"), emptyDefault,
		func(_ context.Context, _ component.Settings, _ component.Config,
			targets map[qdata.Signal]pipeline.Handler,
		) (connector.Connector, error) {
			*captured = targets

			return fakeConnector{Base: dispatcher.Base{}, targets: targets}, nil
		})
}

// pipe builds a PipelineConfig terminated by terminal, fed by acceptors.
func pipe(terminal string, acceptors ...string) querier.PipelineConfig {
	return querier.PipelineConfig{Acceptors: acceptors, Processors: nil, Dispatchers: []string{terminal}}
}

func buildInfo() component.BuildInfo { return component.BuildInfo{Command: "test", Version: "test"} }

func TestBuildWiresConnectorTargetsBySignal(t *testing.T) {
	t.Parallel()

	var captured map[qdata.Signal]pipeline.Handler

	acceptors, err := acceptor.MakeFactoryMap(
		acceptorFactory("otqp"), acceptorFactory("metricsin"), acceptorFactory("logsin"))
	require.NoError(t, err)
	dispatchers, err := dispatcher.MakeFactoryMap(dispatcherFactory("prometheus"), dispatcherFactory("loki"))
	require.NoError(t, err)
	connectors, err := connector.MakeFactoryMap(recordingConnFactory(&captured))
	require.NoError(t, err)

	factories := querier.Factories{
		Acceptors: acceptors, Processors: nil, Dispatchers: dispatchers, Connectors: connectors,
	}

	cfg := &querier.Config{
		Acceptors:   map[string]map[string]any{"otqp": {}, "metricsin": {}, "logsin": {}},
		Processors:  nil,
		Dispatchers: map[string]map[string]any{"prometheus": {}, "loki": {}},
		Connectors:  map[string]map[string]any{"crosssignal": {}},
		Service: querier.ServiceConfig{Pipelines: map[string]querier.PipelineConfig{
			"metrics":    pipe("prometheus", "metricsin", "crosssignal"),
			"logs":       pipe("loki", "logsin", "crosssignal"),
			"logs/cross": pipe("crosssignal", "otqp"),
		}},
	}

	svc, err := querier.Build(factories, cfg, buildInfo())
	require.NoError(t, err)
	require.NotNil(t, svc)

	require.Len(t, captured, 2, "connector is wired to the metrics and logs pipelines")
	assert.Contains(t, captured, qdata.SignalMetrics)
	assert.Contains(t, captured, qdata.SignalLogs)
}

func TestBuildRejectsNestedConnectorTarget(t *testing.T) {
	t.Parallel()

	var captured map[qdata.Signal]pipeline.Handler

	acceptors, err := acceptor.MakeFactoryMap(acceptorFactory("otqp"), acceptorFactory("metricsin"))
	require.NoError(t, err)
	dispatchers, err := dispatcher.MakeFactoryMap(dispatcherFactory("prometheus"))
	require.NoError(t, err)
	connectors, err := connector.MakeFactoryMap(recordingConnFactory(&captured))
	require.NoError(t, err)

	factories := querier.Factories{
		Acceptors: acceptors, Processors: nil, Dispatchers: dispatchers, Connectors: connectors,
	}

	// "logs/cross" is connector-terminated yet lists the connector as its own
	// source — a nested connector target, which must be rejected.
	cfg := &querier.Config{
		Acceptors:   map[string]map[string]any{"otqp": {}, "metricsin": {}},
		Processors:  nil,
		Dispatchers: map[string]map[string]any{"prometheus": {}},
		Connectors:  map[string]map[string]any{"crosssignal": {}},
		Service: querier.ServiceConfig{Pipelines: map[string]querier.PipelineConfig{
			"metrics":    pipe("prometheus", "metricsin"),
			"logs/cross": pipe("crosssignal", "otqp", "crosssignal"),
		}},
	}

	_, err = querier.Build(factories, cfg, buildInfo())
	require.ErrorContains(t, err, "leaf pipeline")
}
