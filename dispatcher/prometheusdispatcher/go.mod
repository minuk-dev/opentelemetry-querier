module github.com/minuk-dev/opentelemetry-querier/dispatcher/prometheusdispatcher

go 1.25.4

require (
	github.com/minuk-dev/opentelemetry-querier/component v0.0.0
	github.com/minuk-dev/opentelemetry-querier/dispatcher v0.0.0
	github.com/minuk-dev/opentelemetry-querier/gen v0.0.0
	github.com/minuk-dev/opentelemetry-querier/qdata v0.0.0
	github.com/minuk-dev/opentelemetry-querier/qerror v0.0.0
	github.com/stretchr/testify v1.11.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/rogpeppe/go-internal v1.10.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/minuk-dev/opentelemetry-querier/component => ../../component

replace github.com/minuk-dev/opentelemetry-querier/dispatcher => ..

replace github.com/minuk-dev/opentelemetry-querier/gen => ../../gen

replace github.com/minuk-dev/opentelemetry-querier/qdata => ../../qdata

replace github.com/minuk-dev/opentelemetry-querier/qerror => ../../qerror
