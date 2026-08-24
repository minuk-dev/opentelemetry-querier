module github.com/minuk-dev/opentelemetry-querier/qdata

go 1.25.4

require (
	github.com/minuk-dev/opentelemetry-querier/gen v0.0.0
	github.com/samber/lo v1.53.0
	github.com/stretchr/testify v1.12.1
	google.golang.org/protobuf v1.36.12
)

require (
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/text v0.38.0 // indirect
)

replace github.com/minuk-dev/opentelemetry-querier/gen => ../gen
