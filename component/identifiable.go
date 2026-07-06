package component

import (
	"fmt"
	"strings"
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
func NewID(typ Type) ID { return ID{typ: typ} }

// NewIDWithName builds a named ID.
func NewIDWithName(typ Type, name string) ID { return ID{typ: typ, name: name} }

// Type returns the component type.
func (id ID) Type() Type { return id.typ }

// Name returns the instance name (may be empty).
func (id ID) Name() string { return id.name }

// String renders the ID as "type" or "type/name".
func (id ID) String() string {
	if id.name == "" {
		return id.typ.String()
	}
	return id.typ.String() + "/" + id.name
}

// UnmarshalText parses "type" or "type/name" (implements encoding.TextUnmarshaler
// so YAML map keys decode directly into an ID).
func (id *ID) UnmarshalText(text []byte) error {
	s := string(text)
	typeStr, nameStr, hasName := strings.Cut(s, "/")
	typeStr = strings.TrimSpace(typeStr)
	if typeStr == "" {
		return fmt.Errorf("component id %q: missing type", s)
	}
	typ, err := NewType(typeStr)
	if err != nil {
		return err
	}
	if hasName {
		nameStr = strings.TrimSpace(nameStr)
		if nameStr == "" {
			return fmt.Errorf("component id %q: empty name after '/'", s)
		}
	}
	id.typ = typ
	id.name = nameStr
	return nil
}

// MarshalText renders the ID for YAML/JSON encoding.
func (id ID) MarshalText() ([]byte, error) { return []byte(id.String()), nil }
