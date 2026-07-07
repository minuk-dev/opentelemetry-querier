package component

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// errMissingType is returned when a component ID has no type part.
	errMissingType = errors.New("component: id missing type")
	// errEmptyName is returned when a component ID has an empty name after '/'.
	errEmptyName = errors.New("component: id has empty name after '/'")
)

// ID identifies a component instance by its Type and an optional Name, matching
// component.ID. The textual form is "type" or "type/name", so a config can
// declare multiple instances of the same type (e.g. "otqp/public",
// "otqp/internal").
type ID struct {
	typ  Type
	name string
}

// NewID builds an ID with an empty name.
func NewID(typ Type) ID { return ID{typ: typ, name: ""} }

// NewIDWithName builds a named ID.
func NewIDWithName(typ Type, name string) ID { return ID{typ: typ, name: name} }

// Type returns the component type.
func (id *ID) Type() Type { return id.typ }

// Name returns the instance name (may be empty).
func (id *ID) Name() string { return id.name }

// String renders the ID as "type" or "type/name".
func (id *ID) String() string {
	if id.name == "" {
		return id.typ.String()
	}

	return id.typ.String() + "/" + id.name
}

// UnmarshalText parses "type" or "type/name" (implements encoding.TextUnmarshaler
// so config values decode directly into an ID).
func (id *ID) UnmarshalText(text []byte) error {
	raw := string(text)

	typeStr, nameStr, hasName := strings.Cut(raw, "/")

	typeStr = strings.TrimSpace(typeStr)
	if typeStr == "" {
		return fmt.Errorf("%w: %q", errMissingType, raw)
	}

	typ, err := NewType(typeStr)
	if err != nil {
		return err
	}

	if hasName {
		nameStr = strings.TrimSpace(nameStr)
		if nameStr == "" {
			return fmt.Errorf("%w: %q", errEmptyName, raw)
		}
	}

	id.typ = typ
	id.name = nameStr

	return nil
}

// MarshalText renders the ID for text encoding.
func (id *ID) MarshalText() ([]byte, error) { return []byte(id.String()), nil }
