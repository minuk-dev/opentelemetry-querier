// Package component mirrors go.opentelemetry.io/collector/component: it defines
// the shared vocabulary every querier component is built from — Type, ID,
// Component lifecycle, Config, Factory and per-instance Settings. Acceptors,
// processors and dispatchers all build on these so a distribution can be
// composed from independently-authored packages.
package component

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
)

// Component is the lifecycle contract shared by every querier component,
// matching component.Component in the collector.
type Component interface {
	// Start begins operation. Acceptors bind listeners here; processors and
	// dispatchers typically no-op.
	Start(ctx context.Context, host Host) error
	// Shutdown stops operation and releases resources.
	Shutdown(ctx context.Context) error
}

// Host exposes the running querier to its components. It is intentionally minimal
// for now, mirroring the collector's component.Host extension point.
type Host interface {
	// GetComponent is a placeholder for future host services (extensions,
	// telemetry). It exists so the interface can grow without breaking callers.
	GetComponent(kind, name string) any
}

// StartFunc is an adapter so a component can supply only a Start implementation.
type StartFunc func(ctx context.Context, host Host) error

// Start calls the wrapped func, treating a nil func as a no-op.
func (f StartFunc) Start(ctx context.Context, host Host) error {
	if f == nil {
		return nil
	}
	return f(ctx, host)
}

// ShutdownFunc is an adapter so a component can supply only a Shutdown impl.
type ShutdownFunc func(ctx context.Context) error

// Shutdown calls the wrapped func, treating a nil func as a no-op.
func (f ShutdownFunc) Shutdown(ctx context.Context) error {
	if f == nil {
		return nil
	}
	return f(ctx)
}

// Config is the marker type for a component's configuration, matching
// component.Config. Concrete configs are plain structs decoded from YAML.
type Config any

// Validator is optionally implemented by a Config to validate itself after
// decoding (cf. the collector's xconfmap.Validator).
type Validator interface {
	Validate() error
}

// ValidateConfig runs Config.Validate if implemented.
func ValidateConfig(cfg Config) error {
	if v, ok := cfg.(Validator); ok {
		return v.Validate()
	}
	return nil
}

// BuildInfo describes the running distribution, surfaced to components via
// Settings (cf. component.BuildInfo).
type BuildInfo struct {
	Command string
	Version string
}

// Settings carries per-instance context handed to a factory's Create* method
// (cf. receiver.Settings / processor.Settings).
type Settings struct {
	// ID is the component instance identifier (type + name).
	ID ID
	// Logger is a component-scoped logger.
	Logger *slog.Logger
	// BuildInfo describes the distribution.
	BuildInfo BuildInfo
}

// Factory is the base factory contract shared by every component category,
// mirroring component.Factory. Category factories embed it and add a Create*
// method.
type Factory interface {
	// Type returns the component type this factory produces.
	Type() Type
	// CreateDefaultConfig returns a fresh, fully-defaulted config value that YAML
	// is decoded into.
	CreateDefaultConfig() Config

	unexportedFactory()
}

// BaseFactory is embedded by category factories to satisfy the sealed Factory
// interface; it is not used directly.
type BaseFactory struct{}

func (BaseFactory) unexportedFactory() {}

// typeRegexp constrains component type names, matching the collector's rule
// (lowercase alphanumerics and underscores, starting with a letter).
var typeRegexp = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Type is a validated component type name (e.g. "otqp", "queryrewrite").
type Type struct {
	name string
}

// String returns the type name.
func (t Type) String() string { return t.name }

// NewType validates and constructs a Type.
func NewType(name string) (Type, error) {
	if !typeRegexp.MatchString(name) {
		return Type{}, fmt.Errorf("invalid component type %q: must match %s", name, typeRegexp.String())
	}
	return Type{name: name}, nil
}

// MustNewType panics on an invalid type; use it for package-level factory types.
func MustNewType(name string) Type {
	t, err := NewType(name)
	if err != nil {
		panic(err)
	}
	return t
}
