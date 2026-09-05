module github.com/minuk-dev/opentelemetry-querier/acceptor/elasticsearchacceptor

go 1.25.4

require (
	github.com/minuk-dev/opentelemetry-querier/acceptor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/component v0.0.0
	github.com/minuk-dev/opentelemetry-querier/gen v0.0.0
	github.com/minuk-dev/opentelemetry-querier/pipeline v0.0.0
	github.com/minuk-dev/opentelemetry-querier/qdata v0.0.0
	github.com/minuk-dev/opentelemetry-querier/qerror v0.0.0
	github.com/stretchr/testify v1.12.1
	google.golang.org/protobuf v1.36.12
)

require go.yaml.in/yaml/v3 v3.0.5 // indirect

require (
	github.com/minuk-dev/opentelemetry-querier/dispatcher v0.0.0 // indirect
	github.com/minuk-dev/opentelemetry-querier/dispatcher/elasticsearchdispatcher v0.0.0
	github.com/minuk-dev/opentelemetry-querier/processor v0.0.0 // indirect
	github.com/samber/lo v1.53.0 // indirect
	golang.org/x/text v0.38.0 // indirect
)

replace github.com/minuk-dev/opentelemetry-querier/acceptor => ..

replace github.com/minuk-dev/opentelemetry-querier/component => ../../component

replace github.com/minuk-dev/opentelemetry-querier/dispatcher => ../../dispatcher

replace github.com/minuk-dev/opentelemetry-querier/gen => ../../gen

replace github.com/minuk-dev/opentelemetry-querier/pipeline => ../../pipeline

replace github.com/minuk-dev/opentelemetry-querier/processor => ../../processor

replace github.com/minuk-dev/opentelemetry-querier/qdata => ../../qdata

replace github.com/minuk-dev/opentelemetry-querier/qerror => ../../qerror

replace github.com/minuk-dev/opentelemetry-querier/dispatcher/elasticsearchdispatcher => ../../dispatcher/elasticsearchdispatcher
