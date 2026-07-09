module github.com/minuk-dev/opentelemetry-querier/dispatcher/promdispatcher

go 1.25.4

require (
	github.com/minuk-dev/opentelemetry-querier/component v0.0.0
	github.com/minuk-dev/opentelemetry-querier/dispatcher v0.0.0
	github.com/minuk-dev/opentelemetry-querier/gen v0.0.0
	github.com/minuk-dev/opentelemetry-querier/qdata v0.0.0
	github.com/minuk-dev/opentelemetry-querier/qerror v0.0.0
	google.golang.org/protobuf v1.36.11
)

replace github.com/minuk-dev/opentelemetry-querier/component => ../../component

replace github.com/minuk-dev/opentelemetry-querier/dispatcher => ..

replace github.com/minuk-dev/opentelemetry-querier/gen => ../../gen

replace github.com/minuk-dev/opentelemetry-querier/qdata => ../../qdata

replace github.com/minuk-dev/opentelemetry-querier/qerror => ../../qerror
