module github.com/minuk-dev/opentelemetry-querier/processor

go 1.25.4

require (
	github.com/minuk-dev/opentelemetry-querier/component v0.0.0-20261118130729-ccf10be9c110
	github.com/minuk-dev/opentelemetry-querier/qdata v0.0.0-20261118130729-ccf10be9c110
)

require (
	github.com/minuk-dev/opentelemetry-querier/gen v0.0.0-20261118130729-ccf10be9c110 // indirect
	github.com/samber/lo v1.53.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/minuk-dev/opentelemetry-querier/component => ../component

replace github.com/minuk-dev/opentelemetry-querier/gen => ../gen

replace github.com/minuk-dev/opentelemetry-querier/qdata => ../qdata
