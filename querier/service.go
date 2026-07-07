package querier

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/minuk-dev/opentelemetry-querier/acceptor"
	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/processor"
)

var (
	// errAcceptorReused is returned when an acceptor feeds more than one pipeline.
	errAcceptorReused = errors.New("querier: acceptor may feed only one pipeline")
	// errUnknownProcessor is returned for a processor type with no factory.
	errUnknownProcessor = errors.New("querier: unknown processor type")
	// errUnknownDispatcher is returned for a dispatcher type with no factory.
	errUnknownDispatcher = errors.New("querier: unknown dispatcher type")
	// errUnknownAcceptor is returned for an acceptor type with no factory.
	errUnknownAcceptor = errors.New("querier: unknown acceptor type")
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
func Build(factories Factories, cfg *Config, buildInfo component.BuildInfo) (*Service, error) {
	svc := &Service{
		buildInfo: buildInfo,
		logger:    slog.Default(),
		pipelines: nil,
		acceptors: nil,
		host:      host{},
	}

	// An acceptor instance may feed exactly one pipeline; enforce that so a
	// single listener has an unambiguous destination.
	acceptorOwner := map[string]string{}

	for name, pipelineCfg := range cfg.Service.Pipelines {
		procs, err := svc.buildProcessors(factories, cfg, name, pipelineCfg.Processors)
		if err != nil {
			return nil, err
		}

		disp, err := svc.buildDispatcher(factories, cfg, pipelineCfg.Dispatchers[0])
		if err != nil {
			return nil, err
		}

		pipe := pipeline.New(name, procs, disp)
		svc.pipelines = append(svc.pipelines, pipe)

		for _, accStr := range pipelineCfg.Acceptors {
			if prev, used := acceptorOwner[accStr]; used {
				return nil, fmt.Errorf("%w: %q used by %q and %q", errAcceptorReused, accStr, prev, name)
			}

			acceptorOwner[accStr] = name

			acc, err := svc.buildAcceptor(factories, cfg, accStr, pipe)
			if err != nil {
				return nil, err
			}

			svc.acceptors = append(svc.acceptors, acc)
		}
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
