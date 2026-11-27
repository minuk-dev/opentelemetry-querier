package querier_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/minuk-dev/opentelemetry-querier/acceptor"
	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/component/extension"
	"github.com/minuk-dev/opentelemetry-querier/connector"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/processor"
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
	return querier.PipelineConfig{QueryTimeout: 0, Acceptors: acceptors, Processors: nil, Dispatchers: []string{terminal}}
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
		Extensions: nil,
		Acceptors:  acceptors, Processors: nil, Dispatchers: dispatchers, Connectors: connectors,
	}

	cfg := &querier.Config{
		Extensions:  nil,
		Acceptors:   map[string]map[string]any{"otqp": {}, "metricsin": {}, "logsin": {}},
		Processors:  nil,
		Dispatchers: map[string]map[string]any{"prometheus": {}, "loki": {}},
		Connectors:  map[string]map[string]any{"crosssignal": {}},
		Service: querier.ServiceConfig{
			Telemetry: false, Concurrency: pipeline.ConcurrencyConfig{PerTenant: 0, Backends: nil, QueueSize: 0},
			Extensions: nil, Pipelines: map[string]querier.PipelineConfig{
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
		Extensions: nil,
		Acceptors:  acceptors, Processors: nil, Dispatchers: dispatchers, Connectors: connectors,
	}

	// "logs/cross" is connector-terminated yet lists the connector as its own
	// source — a nested connector target, which must be rejected.
	cfg := &querier.Config{
		Extensions:  nil,
		Acceptors:   map[string]map[string]any{"otqp": {}, "metricsin": {}},
		Processors:  nil,
		Dispatchers: map[string]map[string]any{"prometheus": {}},
		Connectors:  map[string]map[string]any{"crosssignal": {}},
		Service: querier.ServiceConfig{
			Telemetry: false, Concurrency: pipeline.ConcurrencyConfig{PerTenant: 0, Backends: nil, QueueSize: 0},
			Extensions: nil, Pipelines: map[string]querier.PipelineConfig{
				"metrics":    pipe("prometheus", "metricsin"),
				"logs/cross": pipe("crosssignal", "otqp", "crosssignal"),
			}},
	}

	_, err = querier.Build(factories, cfg, buildInfo())
	require.ErrorContains(t, err, "leaf pipeline")
}

type tenantConsumer struct {
	processor.Base
}

func (tenantConsumer) SecurityRequirements() processor.SecurityRequirements {
	return processor.SecurityRequirements{
		ResolvesTenant: false, NeedsTenant: true, RegistersEnforcement: false, AppliesEnforcement: false,
	}
}

func TestBuildRejectsNamedSecurityDependency(t *testing.T) {
	t.Parallel()

	proc := processor.NewFactory(component.MustNewType("consumer"), emptyDefault,
		func(context.Context, component.Settings, component.Config) (processor.Processor, error) {
			return tenantConsumer{Base: processor.Base{}}, nil
		})
	factories := querier.Factories{
		Extensions: nil,
		Acceptors:  map[component.Type]acceptor.Factory{component.MustNewType("otqp"): acceptorFactory("otqp")},
		Processors: map[component.Type]processor.Factory{component.MustNewType("consumer"): proc},
		Dispatchers: map[component.Type]dispatcher.Factory{
			component.MustNewType("prometheus"): dispatcherFactory("prometheus"),
		},
		Connectors: nil,
	}
	cfg := &querier.Config{
		Extensions:  nil,
		Acceptors:   map[string]map[string]any{"otqp": {}},
		Processors:  map[string]map[string]any{"consumer/tenant-quota": {}},
		Dispatchers: map[string]map[string]any{"prometheus": {}}, Connectors: nil,
		Service: querier.ServiceConfig{
			Telemetry: false, Concurrency: pipeline.ConcurrencyConfig{PerTenant: 0, Backends: nil, QueueSize: 0},
			Extensions: nil, Pipelines: map[string]querier.PipelineConfig{
				"metrics/private": {QueryTimeout: 0, Acceptors: []string{"otqp"}, Processors: []string{"consumer/tenant-quota"},
					Dispatchers: []string{"prometheus"}},
			}},
	}
	_, err := querier.Build(factories, cfg, buildInfo())
	require.ErrorContains(t, err, "tenant resolver")
	assert.Contains(t, err.Error(), "metrics/private")
	assert.Contains(t, err.Error(), "consumer/tenant-quota")
}

func TestBuildRejectsDuplicateConnectorTargets(t *testing.T) {
	t.Parallel()

	var captured map[qdata.Signal]pipeline.Handler

	dispatchers, err := dispatcher.MakeFactoryMap(dispatcherFactory("prometheus"))
	require.NoError(t, err)
	connectors, err := connector.MakeFactoryMap(recordingConnFactory(&captured))
	require.NoError(t, err)

	factories := querier.Factories{
		Extensions: nil, Acceptors: nil, Processors: nil, Dispatchers: dispatchers, Connectors: connectors}
	cfg := &querier.Config{
		Extensions: nil,
		Acceptors:  nil, Processors: nil, Dispatchers: nil,
		Connectors: map[string]map[string]any{"crosssignal/a": {}},
		Service: querier.ServiceConfig{
			Telemetry: false, Concurrency: pipeline.ConcurrencyConfig{PerTenant: 0, Backends: nil, QueueSize: 0},
			Extensions: nil, Pipelines: map[string]querier.PipelineConfig{
				"metrics/a":    pipe("prometheus", "crosssignal/a"),
				"metrics/b":    pipe("prometheus", "crosssignal/a"),
				"metrics/join": pipe("crosssignal/a", "otqp"),
			}},
	}

	_, err = querier.Build(factories, cfg, buildInfo())
	require.ErrorContains(t, err, "connector has multiple targets for one signal")
	require.ErrorContains(t, err, "crosssignal/a")
	require.ErrorContains(t, err, "metrics/a")
	require.ErrorContains(t, err, "metrics/b")
}

func TestBuildBindsEnabledServerAuthenticator(t *testing.T) {
	t.Parallel()

	var handler pipeline.Handler

	input := component.MustNewType("input")
	verify := component.MustNewType("verify")
	factories := querier.Factories{
		Acceptors: map[component.Type]acceptor.Factory{
			input: acceptor.NewFactory(input, emptyDefault, func(
				_ context.Context, _ component.Settings, _ component.Config, next pipeline.Handler,
			) (acceptor.Acceptor, error) {
				handler = next

				return fakeAcceptor{}, nil
			}),
		},
		Extensions: map[component.Type]extension.Factory{
			verify: extension.NewFactory(verify, emptyDefault, func(
				context.Context, component.Settings, component.Config,
			) (extension.Extension, error) {
				return acceptor.AuthenticatorFunc(func(_ context.Context, headers http.Header) (acceptor.Principal, error) {
					return acceptor.Principal{
						Identity: qdata.Identity{
							Authority: "test", Principal: headers.Get("X-Test-Principal"), Tenant: "", Groups: nil, Roles: nil, Scopes: nil,
						},
						Tenants: []string{"acme"},
					}, nil
				}), nil
			}),
		},
		Dispatchers: map[component.Type]dispatcher.Factory{component.MustNewType("backend"): dispatcherFactory("backend")},
		Processors:  nil, Connectors: nil,
	}
	config := &querier.Config{
		Acceptors:   map[string]map[string]any{"input": {"auth": map[string]any{"authenticator": "verify/client"}}},
		Extensions:  map[string]map[string]any{"verify/client": {}},
		Dispatchers: map[string]map[string]any{"backend": {}},
		Processors:  nil, Connectors: nil,
		Service: querier.ServiceConfig{
			Telemetry: false, Concurrency: pipeline.ConcurrencyConfig{PerTenant: 0, Backends: nil, QueueSize: 0},
			Extensions: nil, Pipelines: map[string]querier.PipelineConfig{"metrics": pipe("backend", "input")},
		},
	}
	_, err := querier.Build(factories, config, buildInfo())
	require.ErrorContains(t, err, "expected an enabled server authenticator")

	config.Service.Extensions = []string{"verify/client"}
	service, err := querier.Build(factories, config, buildInfo())
	require.NoError(t, err)
	require.NoError(t, service.Start(t.Context()))
	t.Cleanup(func() { assert.NoError(t, service.Shutdown(context.Background())) })

	query := &qdata.Query{}
	acceptor.PrepareIngress(query, http.Header{"X-Test-Principal": {"alice"}})
	_, err = handler.Handle(t.Context(), query)
	require.NoError(t, err)
	assert.Equal(t, "acme", qdata.TenantID(query))
	acceptor.PrepareIngress(query, http.Header{"X-Test-Principal": {"alice"}, "X-Scope-Orgid": {"other"}})
	_, err = handler.Handle(t.Context(), query)
	require.ErrorContains(t, err, "tenant outside verified scope")
}
