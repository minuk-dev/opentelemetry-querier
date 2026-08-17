module github.com/minuk-dev/opentelemetry-querier/querier

go 1.25.4

require (
	github.com/go-viper/mapstructure/v2 v2.5.0
	github.com/minuk-dev/opentelemetry-querier/acceptor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/component v0.0.0
	github.com/minuk-dev/opentelemetry-querier/connector v0.0.0-00010101000000-000000000000
	github.com/minuk-dev/opentelemetry-querier/dispatcher v0.0.0
	github.com/minuk-dev/opentelemetry-querier/pipeline v0.0.0
	github.com/minuk-dev/opentelemetry-querier/processor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/qdata v0.0.0
	github.com/stretchr/testify v1.11.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/minuk-dev/opentelemetry-querier/gen v0.0.0 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/samber/lo v1.53.0 // indirect
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
