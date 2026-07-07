// Package querier is the assembly layer, mirroring
// go.opentelemetry.io/collector/otelcol. It holds the set of component factories
// available in a distribution (Factories), loads the two-layer configuration,
// and builds the configured pipelines into a runnable Service.
package querier

import (
	"github.com/minuk-dev/opentelemetry-querier/acceptor"
	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/dispatcher"
	"github.com/minuk-dev/opentelemetry-querier/processor"
)

// Factories is the set of component factories compiled into a distribution. A
// builder-generated components.go populates this (cf. otelcol.Factories).
type Factories struct {
	Acceptors   map[component.Type]acceptor.Factory
	Processors  map[component.Type]processor.Factory
	Dispatchers map[component.Type]dispatcher.Factory
}
