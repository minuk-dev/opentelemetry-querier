package simpleauthz

import (
	"context"
	"errors"
	"fmt"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/processor"
)

var (
	// errInvalidConfig is returned when the factory receives an unexpected config type.
	errInvalidConfig = errors.New("simpleauthz: invalid config type")
	// errUnknownEffect is returned for an effect that is neither allow nor deny.
	errUnknownEffect = errors.New("simpleauthz: unknown effect")
	// errLabelNameEmpty is returned for an enforce_label with no name.
	errLabelNameEmpty = errors.New("simpleauthz: enforce_label name is required")
)

// NewFactory returns the factory for the simple authorization processor.
func NewFactory() processor.Factory {
	return processor.NewFactory(
		component.MustNewType("simpleauthz"),
		createDefaultConfig,
		createProcessor,
	)
}

func createDefaultConfig() component.Config {
	return &Config{
		SubjectHeader:  DefaultSubjectHeader,
		FromTenant:     false,
		DefaultSubject: "",
		Required:       false,
		DefaultEffect:  EffectDeny,
		Rules:          nil,
	}
}

func createProcessor(_ context.Context, _ component.Settings, cfg component.Config) (processor.Processor, error) {
	conf, ok := cfg.(*Config)
	if !ok {
		return nil, errInvalidConfig
	}

	err := Validate(*conf)
	if err != nil {
		return nil, err
	}

	return New(*conf), nil
}

// Validate rejects a policy that would behave surprisingly: an unknown effect
// (which must never silently allow), or an enforce_label with no name. It is
// called by the factory so misconfiguration fails at startup, not per query.
func Validate(cfg Config) error {
	err := validateEffect("default_effect", cfg.DefaultEffect)
	if err != nil {
		return err
	}

	for index, rule := range cfg.Rules {
		err = validateEffect(fmt.Sprintf("rules[%d].effect", index), rule.Effect)
		if err != nil {
			return err
		}

		for labelIndex, label := range rule.EnforceLabels {
			if label.Name == "" {
				return fmt.Errorf("%w: rules[%d].enforce_labels[%d]", errLabelNameEmpty, index, labelIndex)
			}
		}
	}

	return nil
}

// validateEffect accepts "allow", "deny", or empty (which defaults per field);
// anything else is a typo that could misroute an authorization decision.
func validateEffect(field, effect string) error {
	switch effect {
	case "", EffectAllow, EffectDeny:
		return nil
	default:
		return fmt.Errorf("%w: %s: %q (want %q or %q)",
			errUnknownEffect, field, effect, EffectAllow, EffectDeny)
	}
}
