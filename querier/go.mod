module github.com/minuk-dev/opentelemetry-querier/querier

go 1.25.4

require (
	github.com/go-viper/mapstructure/v2 v2.5.0
	github.com/minuk-dev/opentelemetry-querier/acceptor v0.1.0
	github.com/minuk-dev/opentelemetry-querier/component v0.1.0
	github.com/minuk-dev/opentelemetry-querier/connector v0.1.0
	github.com/minuk-dev/opentelemetry-querier/dispatcher v0.1.0
	github.com/minuk-dev/opentelemetry-querier/pipeline v0.1.0
	github.com/minuk-dev/opentelemetry-querier/processor v0.1.0
	github.com/minuk-dev/opentelemetry-querier/qdata v0.1.0
	github.com/minuk-dev/opentelemetry-querier/qerror v0.1.0
	github.com/redis/go-redis/v9 v9.22.0
	github.com/stretchr/testify v1.12.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/minuk-dev/opentelemetry-querier/gen v0.1.0 // indirect
	github.com/samber/lo v1.53.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/text v0.38.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/minuk-dev/opentelemetry-querier/acceptor => ../acceptor

replace github.com/minuk-dev/opentelemetry-querier/component => ../component

replace github.com/minuk-dev/opentelemetry-querier/dispatcher => ../dispatcher

replace github.com/minuk-dev/opentelemetry-querier/gen => ../gen

replace github.com/minuk-dev/opentelemetry-querier/pipeline => ../pipeline

replace github.com/minuk-dev/opentelemetry-querier/processor => ../processor

replace github.com/minuk-dev/opentelemetry-querier/qdata => ../qdata

replace github.com/minuk-dev/opentelemetry-querier/connector => ../connector

replace github.com/minuk-dev/opentelemetry-querier/qerror => ../qerror
