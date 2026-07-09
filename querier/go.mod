module github.com/minuk-dev/opentelemetry-querier/querier

go 1.25.4

require (
	github.com/go-viper/mapstructure/v2 v2.5.0
	github.com/minuk-dev/opentelemetry-querier/acceptor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/component v0.0.0
	github.com/minuk-dev/opentelemetry-querier/dispatcher v0.0.0
	github.com/minuk-dev/opentelemetry-querier/pipeline v0.0.0
	github.com/minuk-dev/opentelemetry-querier/processor v0.0.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/minuk-dev/opentelemetry-querier/gen v0.0.0 // indirect
	github.com/minuk-dev/opentelemetry-querier/qdata v0.0.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/minuk-dev/opentelemetry-querier/acceptor => ../acceptor

replace github.com/minuk-dev/opentelemetry-querier/component => ../component

replace github.com/minuk-dev/opentelemetry-querier/dispatcher => ../dispatcher

replace github.com/minuk-dev/opentelemetry-querier/gen => ../gen

replace github.com/minuk-dev/opentelemetry-querier/pipeline => ../pipeline

replace github.com/minuk-dev/opentelemetry-querier/processor => ../processor

replace github.com/minuk-dev/opentelemetry-querier/qdata => ../qdata
