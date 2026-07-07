module github.com/minuk-dev/opentelemetry-querier/acceptor/otqp

go 1.25.4

require (
	github.com/minuk-dev/opentelemetry-querier/acceptor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/component v0.0.0
	github.com/minuk-dev/opentelemetry-querier/gen v0.0.0
	github.com/minuk-dev/opentelemetry-querier/pipeline v0.0.0
	github.com/minuk-dev/opentelemetry-querier/qdata v0.0.0
	github.com/minuk-dev/opentelemetry-querier/qerror v0.0.0
	google.golang.org/grpc v1.66.0
	google.golang.org/protobuf v1.34.2
)

require (
	github.com/minuk-dev/opentelemetry-querier/dispatcher v0.0.0 // indirect
	github.com/minuk-dev/opentelemetry-querier/processor v0.0.0 // indirect
	golang.org/x/net v0.26.0 // indirect
	golang.org/x/sys v0.21.0 // indirect
	golang.org/x/text v0.16.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240604185151-ef581f913117 // indirect
)

replace github.com/minuk-dev/opentelemetry-querier/acceptor => ..

replace github.com/minuk-dev/opentelemetry-querier/component => ../../component

replace github.com/minuk-dev/opentelemetry-querier/dispatcher => ../../dispatcher

replace github.com/minuk-dev/opentelemetry-querier/gen => ../../gen

replace github.com/minuk-dev/opentelemetry-querier/pipeline => ../../pipeline

replace github.com/minuk-dev/opentelemetry-querier/processor => ../../processor

replace github.com/minuk-dev/opentelemetry-querier/qdata => ../../qdata

replace github.com/minuk-dev/opentelemetry-querier/qerror => ../../qerror
