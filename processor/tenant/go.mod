module github.com/minuk-dev/opentelemetry-querier/processor/tenant

go 1.25.4

require (
	github.com/minuk-dev/opentelemetry-querier/component v0.0.0
	github.com/minuk-dev/opentelemetry-querier/processor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/qdata v0.0.0
	github.com/minuk-dev/opentelemetry-querier/qerror v0.0.0
)

require (
	github.com/minuk-dev/opentelemetry-querier/gen v0.0.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/minuk-dev/opentelemetry-querier/component => ../../component

replace github.com/minuk-dev/opentelemetry-querier/gen => ../../gen

replace github.com/minuk-dev/opentelemetry-querier/processor => ..

replace github.com/minuk-dev/opentelemetry-querier/qdata => ../../qdata

replace github.com/minuk-dev/opentelemetry-querier/qerror => ../../qerror
