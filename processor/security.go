package processor

import (
	"errors"
	"fmt"
)

// SecurityRequirements describes dependencies of a configured processor.
type SecurityRequirements struct {
	ResolvesTenant       bool
	NeedsTenant          bool
	RegistersEnforcement bool
	AppliesEnforcement   bool
}

// SecurityProcessor declares security dependencies without tying the pipeline
// to concrete processor implementations. Custom security processors should
// implement this interface as well as Processor.
type SecurityProcessor interface {
	SecurityRequirements() SecurityRequirements
}

var errSecurityOrder = errors.New("invalid security processor order")

// ValidateOrder rejects unresolved tenant dependencies and enforcement that has
// no subsequent query rewrite. Stateless processors need not declare anything.
func ValidateOrder(processors []Processor) error {
	tenantResolved := false
	pending := -1

	for index, proc := range processors {
		security, ok := proc.(SecurityProcessor)
		if !ok {
			continue
		}

		requirements := security.SecurityRequirements()
		if requirements.NeedsTenant && !tenantResolved {
			return fmt.Errorf("%w: processor %d (%T) requires a preceding tenant resolver", errSecurityOrder, index, proc)
		}

		tenantResolved = tenantResolved || requirements.ResolvesTenant
		if requirements.RegistersEnforcement {
			pending = index
		}

		if requirements.AppliesEnforcement {
			pending = -1
		}
	}

	if pending >= 0 {
		return fmt.Errorf("%w: processor %d (%T) requires a subsequent query rewrite",
			errSecurityOrder, pending, processors[pending])
	}

	return nil
}
