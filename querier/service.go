package querier

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/minuk-dev/opentelemetry-querier/acceptor"
	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/connector"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/processor"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
)

var (
	// errAcceptorReused is returned when an acceptor feeds more than one pipeline.
	errAcceptorReused = errors.New("querier: acceptor may feed only one pipeline")
	// errUnknownProcessor is returned for a processor type with no factory.
	errUnknownProcessor = errors.New("querier: unknown processor type")
	// errUnknownDispatcher is returned for a dispatcher type with no factory.
	errUnknownDispatcher = errors.New("querier: unknown dispatcher type")
	// errUnknownConnector is returned for a connector type with no factory.
	errUnknownConnector = errors.New("querier: unknown connector type")
	// errUnknownAcceptor is returned for an acceptor type with no factory.
	errUnknownAcceptor = errors.New("querier: unknown acceptor type")
	// errConnectorTargetNotLeaf is returned when a connector's target pipeline is
	// itself connector-terminated (nested connectors are unsupported).
	errConnectorTargetNotLeaf = errors.New("querier: connector target must be a leaf pipeline")
)

// Service is a fully assembled, runnable querier: a set of pipelines and the
// acceptors that feed them.
type Service struct {
	buildInfo component.BuildInfo
	logger    *slog.Logger
	pipelines []*pipeline.Pipeline
	acceptors []acceptor.Acceptor
	host      component.Host
}

// host is a minimal component.Host implementation.
type host struct{}

func (host) GetComponent(string, string) any { return nil }

// Build assembles a Service from the compiled factories and the runtime config.
// Pipelines are built in two passes so a connector-terminated pipeline can bind
// to the per-signal target pipelines it fans out to: first the leaf pipelines
// (terminated by a real dispatcher), then the connector-terminated ones. Finally
// the transport acceptors are wired; a connector referenced in a pipeline's
// acceptors list is a connector-source, not a transport acceptor, so it is
// resolved as a target binding rather than a listener.
func Build(factories Factories, cfg *Config, buildInfo component.BuildInfo) (*Service, error) {
	svc := &Service{
		buildInfo: buildInfo,
		logger:    slog.Default(),
		pipelines: nil,
		acceptors: nil,
		host:      host{},
	}

	built := map[string]*pipeline.Pipeline{}

	err := svc.buildLeafPipelines(factories, cfg, built)
	if err != nil {
		return nil, err
	}

	targets, err := connectorTargets(cfg, built)
	if err != nil {
		return nil, err
	}

	err = svc.buildConnectorPipelines(factories, cfg, built, targets)
	if err != nil {
		return nil, err
	}

	for _, pipe := range built {
		svc.pipelines = append(svc.pipelines, pipe)
	}

	err = svc.wireAcceptors(factories, cfg, built)
	if err != nil {
		return nil, err
	}

	return svc, nil
}

// Start starts every pipeline (dispatchers + processors) then every acceptor.
func (svc *Service) Start(ctx context.Context) error {
	for _, pipe := range svc.pipelines {
		err := pipe.Start(ctx, svc.host)
		if err != nil {
			return fmt.Errorf("querier: start pipeline %q: %w", pipe.Name, err)
		}
	}

	for _, acc := range svc.acceptors {
		err := acc.Start(ctx, svc.host)
		if err != nil {
			return fmt.Errorf("querier: start acceptor: %w", err)
		}
	}

	return nil
}

// Shutdown stops acceptors first, then pipelines.
func (svc *Service) Shutdown(ctx context.Context) error {
	for _, acc := range svc.acceptors {
		_ = acc.Shutdown(ctx)
	}

	for _, pipe := range svc.pipelines {
		_ = pipe.Shutdown(ctx)
	}

	return nil
}

// isConnectorID reports whether idStr names a connector (declared in the
// connectors config section).
func isConnectorID(cfg *Config, idStr string) bool {
	_, ok := cfg.Connectors[idStr]

	return ok
}

// buildLeafPipelines builds every pipeline whose terminal is a real dispatcher.
func (svc *Service) buildLeafPipelines(factories Factories, cfg *Config, built map[string]*pipeline.Pipeline) error {
	for name, pipelineCfg := range cfg.Service.Pipelines {
		if isConnectorID(cfg, pipelineCfg.Dispatchers[0]) {
			continue
		}

		procs, err := svc.buildProcessors(factories, cfg, name, pipelineCfg.Processors)
		if err != nil {
			return err
		}

		disp, err := svc.buildDispatcher(factories, cfg, pipelineCfg.Dispatchers[0])
		if err != nil {
			return err
		}

		built[name] = pipeline.New(name, procs, disp)
	}

	return nil
}

// connectorTargets resolves, for each connector, the per-signal target pipelines
// it fans out to: the pipelines that list it as a connector-source, keyed by each
// pipeline's signal. Targets must be leaf pipelines (built in the first pass).
func connectorTargets(
	cfg *Config,
	built map[string]*pipeline.Pipeline,
) (map[string]map[qdata.Signal]pipeline.Handler, error) {
	targets := map[string]map[qdata.Signal]pipeline.Handler{}

	for name, pipelineCfg := range cfg.Service.Pipelines {
		for _, accStr := range pipelineCfg.Acceptors {
			if !isConnectorID(cfg, accStr) {
				continue
			}

			leaf, ok := built[name]
			if !ok {
				return nil, fmt.Errorf("%w: %q feeds connector %q", errConnectorTargetNotLeaf, name, accStr)
			}

			signal, err := pipelineSignal(name)
			if err != nil {
				return nil, err
			}

			if targets[accStr] == nil {
				targets[accStr] = map[qdata.Signal]pipeline.Handler{}
			}

			targets[accStr][signal] = leaf
		}
	}

	return targets, nil
}

// buildConnectorPipelines builds every pipeline whose terminal is a connector,
// binding the connector to its resolved per-signal target pipelines.
func (svc *Service) buildConnectorPipelines(
	factories Factories,
	cfg *Config,
	built map[string]*pipeline.Pipeline,
	targets map[string]map[qdata.Signal]pipeline.Handler,
) error {
	for name, pipelineCfg := range cfg.Service.Pipelines {
		connID := pipelineCfg.Dispatchers[0]
		if !isConnectorID(cfg, connID) {
			continue
		}

		procs, err := svc.buildProcessors(factories, cfg, name, pipelineCfg.Processors)
		if err != nil {
			return err
		}

		conn, err := svc.buildConnector(factories, cfg, connID, targets[connID])
		if err != nil {
			return err
		}

		built[name] = pipeline.New(name, procs, conn)
	}

	return nil
}

// wireAcceptors binds each pipeline's transport acceptors (skipping connector
// sources), enforcing that an acceptor feeds exactly one pipeline.
func (svc *Service) wireAcceptors(factories Factories, cfg *Config, built map[string]*pipeline.Pipeline) error {
	acceptorOwner := map[string]string{}

	for name, pipelineCfg := range cfg.Service.Pipelines {
		for _, accStr := range pipelineCfg.Acceptors {
			if isConnectorID(cfg, accStr) {
				continue
			}

			if prev, used := acceptorOwner[accStr]; used {
				return fmt.Errorf("%w: %q used by %q and %q", errAcceptorReused, accStr, prev, name)
			}

			acceptorOwner[accStr] = name

			acc, err := svc.buildAcceptor(factories, cfg, accStr, built[name])
			if err != nil {
				return err
			}

			svc.acceptors = append(svc.acceptors, acc)
		}
	}

	return nil
}

// buildConnector builds a connector bound to its per-signal target pipelines.
func (svc *Service) buildConnector(
	factories Factories,
	cfg *Config,
	idStr string,
	targets map[qdata.Signal]pipeline.Handler,
) (connector.Connector, error) {
	id, err := parseID(idStr)
	if err != nil {
		return nil, err
	}

	factory := factories.Connectors[id.Type()]
	if factory == nil {
		return nil, fmt.Errorf("%w %q", errUnknownConnector, id.Type())
	}

	compCfg, err := decodeComponentConfig(cfg.Connectors, idStr, factory.CreateDefaultConfig())
	if err != nil {
		return nil, err
	}

	conn, err := factory.CreateConnector(context.Background(), svc.settings(id), compCfg, targets)
	if err != nil {
		return nil, fmt.Errorf("querier: create connector %q: %w", idStr, err)
	}

	return conn, nil
}

func (svc *Service) buildProcessors(
	factories Factories,
	cfg *Config,
	pipelineName string,
	ids []string,
) ([]processor.Processor, error) {
	out := make([]processor.Processor, 0, len(ids))

	for _, idStr := range ids {
		id, err := parseID(idStr)
		if err != nil {
			return nil, err
		}

		factory := factories.Processors[id.Type()]
		if factory == nil {
			return nil, fmt.Errorf("%w %q in pipeline %q", errUnknownProcessor, id.Type(), pipelineName)
		}

		compCfg, err := decodeComponentConfig(cfg.Processors, idStr, factory.CreateDefaultConfig())
		if err != nil {
			return nil, err
		}

		proc, err := factory.CreateProcessor(context.Background(), svc.settings(id), compCfg)
		if err != nil {
			return nil, fmt.Errorf("querier: create processor %q: %w", idStr, err)
		}

		out = append(out, proc)
	}

	return out, nil
}

func (svc *Service) buildDispatcher(factories Factories, cfg *Config, idStr string) (dispatcher.Dispatcher, error) {
	id, err := parseID(idStr)
	if err != nil {
		return nil, err
	}

	factory := factories.Dispatchers[id.Type()]
	if factory == nil {
		return nil, fmt.Errorf("%w %q", errUnknownDispatcher, id.Type())
	}

	compCfg, err := decodeComponentConfig(cfg.Dispatchers, idStr, factory.CreateDefaultConfig())
	if err != nil {
		return nil, err
	}

	disp, err := factory.CreateDispatcher(context.Background(), svc.settings(id), compCfg)
	if err != nil {
		return nil, fmt.Errorf("querier: create dispatcher %q: %w", idStr, err)
	}

	return disp, nil
}

func (svc *Service) buildAcceptor(
	factories Factories,
	cfg *Config,
	idStr string,
	next pipeline.Handler,
) (acceptor.Acceptor, error) {
	id, err := parseID(idStr)
	if err != nil {
		return nil, err
	}

	factory := factories.Acceptors[id.Type()]
	if factory == nil {
		return nil, fmt.Errorf("%w %q", errUnknownAcceptor, id.Type())
	}

	compCfg, err := decodeComponentConfig(cfg.Acceptors, idStr, factory.CreateDefaultConfig())
	if err != nil {
		return nil, err
	}

	acc, err := factory.CreateAcceptor(context.Background(), svc.settings(id), compCfg, next)
	if err != nil {
		return nil, fmt.Errorf("querier: create acceptor %q: %w", idStr, err)
	}

	return acc, nil
}

func (svc *Service) settings(id component.ID) component.Settings {
	return component.Settings{
		ID:        id,
		Logger:    svc.logger.With("component", id.String()),
		BuildInfo: svc.buildInfo,
	}
}

// decodeComponentConfig decodes the raw settings map for idStr (if any) into the
// factory's default config, then validates it.
func decodeComponentConfig(
	section map[string]map[string]any,
	idStr string,
	def component.Config,
) (component.Config, error) {
	if raw, ok := section[idStr]; ok {
		err := decodeStrict(raw, def)
		if err != nil {
			return nil, fmt.Errorf("querier: decode config for %q: %w", idStr, err)
		}
	}

	err := component.ValidateConfig(def)
	if err != nil {
		return nil, fmt.Errorf("querier: invalid config for %q: %w", idStr, err)
	}

	return def, nil
}
