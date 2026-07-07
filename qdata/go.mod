module github.com/minuk-dev/opentelemetry-querier/qdata

go 1.25.4

require (
	github.com/minuk-dev/opentelemetry-querier/gen v0.0.0
	google.golang.org/protobuf v1.34.2
)

replace github.com/minuk-dev/opentelemetry-querier/gen => ../gen
