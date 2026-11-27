package querier

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"

	"github.com/minuk-dev/opentelemetry-querier/acceptor"
	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
)

var (
	errExtensionNotConfigured   = errors.New("extension is not configured")
	errExtensionDuplicated      = errors.New("extension is enabled twice")
	errExtensionNotCompiled     = errors.New("extension factory is not compiled")
	errAuthenticatorUnavailable = errors.New("expected an enabled server authenticator extension")
)

func (svc *Service) buildExtensions(factories Factories, cfg *Config) error {
	for _, name := range cfg.Service.Extensions {
		if _, exists := svc.host[name]; exists {
			return fmt.Errorf("%w: %q", errExtensionDuplicated, name)
		}

		if _, exists := cfg.Extensions[name]; !exists {
			return fmt.Errorf("%w: %q", errExtensionNotConfigured, name)
		}

		err := svc.buildExtension(factories, cfg, name)
		if err != nil {
			return err
		}
	}

	return nil
}

func (svc *Service) buildExtension(factories Factories, cfg *Config, name string) error {
	id, err := parseID(name)
	if err != nil {
		return err
	}

	factory := factories.Extensions[id.Type()]
	if factory == nil {
		return fmt.Errorf("%w: %q", errExtensionNotCompiled, name)
	}

	conf, err := decodeComponentConfig(cfg.Extensions, name, factory.CreateDefaultConfig())
	if err != nil {
		return err
	}

	instance, err := factory.CreateExtension(context.Background(), svc.settings(id), conf)
	if err != nil {
		return fmt.Errorf("querier: create extension %q: %w", name, err)
	}

	svc.host[name] = instance
	svc.extensionOrder = append(svc.extensionOrder, name)

	return nil
}

func (svc *Service) acceptorConfig(
	raw map[string]any, id string, defaultCfg component.Config, pipe *pipeline.Pipeline,
) (component.Config, pipeline.HandlerFunc, error) {
	config := maps.Clone(raw)
	auth, configured := config["auth"]
	delete(config, "auth")

	comp, err := decodeComponentConfig(map[string]map[string]any{id: config}, id, defaultCfg)
	if err != nil {
		return nil, nil, err
	}

	if !configured {
		return comp, pipe.Handle, nil
	}

	next, err := svc.bindAuthenticator(auth, pipe)
	if err != nil {
		return nil, nil, fmt.Errorf("acceptor %q: %w", id, err)
	}

	return comp, next, nil
}

func (svc *Service) bindAuthenticator(raw any, pipe *pipeline.Pipeline) (pipeline.HandlerFunc, error) {
	var config acceptor.AuthConfig

	err := decodeStrict(raw, &config)
	if err != nil {
		return nil, err
	}

	verifier, ok := svc.host[config.Authenticator].(acceptor.ServerAuthenticator)
	if !ok {
		return nil, fmt.Errorf("%w: %q", errAuthenticatorUnavailable, config.Authenticator)
	}

	traced := tracedAuthenticator{ServerAuthenticator: verifier, pipe: pipe}

	return pipe.ObserveIngress(acceptor.WithAuthentication(pipe, traced, config.TenantHeader, pipe.Timeout)), nil
}

type tracedAuthenticator struct {
	acceptor.ServerAuthenticator

	pipe *pipeline.Pipeline
}

func (a tracedAuthenticator) Authenticate(ctx context.Context, headers http.Header) (acceptor.Principal, error) {
	ctx, finish := a.pipe.StartAuthentication(ctx)
	principal, err := a.ServerAuthenticator.Authenticate(ctx, headers)
	finish(err)

	if err != nil {
		return principal, fmt.Errorf("verify ingress credentials: %w", err)
	}

	return principal, nil
}
