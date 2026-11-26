module github.com/minuk-dev/opentelemetry-querier/pipeline

go 1.25.4

require (
	github.com/minuk-dev/opentelemetry-querier/component v0.1.0
	github.com/minuk-dev/opentelemetry-querier/dispatcher v0.1.0
	github.com/minuk-dev/opentelemetry-querier/processor v0.1.0
	github.com/minuk-dev/opentelemetry-querier/qdata v0.1.0
	github.com/minuk-dev/opentelemetry-querier/qerror v0.1.0
	github.com/stretchr/testify v1.12.1
	go.opentelemetry.io/otel v1.44.0
	go.opentelemetry.io/otel/metric v1.44.0
	go.opentelemetry.io/otel/sdk v1.44.0
	go.opentelemetry.io/otel/sdk/metric v1.44.0
	go.opentelemetry.io/otel/trace v1.44.0
	google.golang.org/protobuf v1.36.12
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/minuk-dev/opentelemetry-querier/gen v0.1.0 // indirect
	github.com/samber/lo v1.53.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

replace github.com/minuk-dev/opentelemetry-querier/component => ../component

replace github.com/minuk-dev/opentelemetry-querier/dispatcher => ../dispatcher

replace github.com/minuk-dev/opentelemetry-querier/gen => ../gen

replace github.com/minuk-dev/opentelemetry-querier/processor => ../processor

replace github.com/minuk-dev/opentelemetry-querier/qdata => ../qdata

replace github.com/minuk-dev/opentelemetry-querier/qerror => ../qerror
