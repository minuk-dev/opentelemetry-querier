module github.com/minuk-dev/opentelemetry-querier/processor/athenzauthzprocessor

go 1.25.4

require (
	github.com/AthenZ/athenz-authorizer/v5 v5.8.1
	github.com/minuk-dev/opentelemetry-querier/component v0.1.0
	github.com/minuk-dev/opentelemetry-querier/extension/athenzauthextension v0.1.0
	github.com/minuk-dev/opentelemetry-querier/processor v0.1.0
	github.com/minuk-dev/opentelemetry-querier/qdata v0.1.0
	github.com/minuk-dev/opentelemetry-querier/qerror v0.1.0
	github.com/stretchr/testify v1.12.1
)

require (
	github.com/AthenZ/athenz v1.11.43 // indirect
	github.com/ardielle/ardielle-go v1.5.2 // indirect
	github.com/goccy/go-json v0.10.5 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/kpango/fastime v1.1.10 // indirect
	github.com/kpango/gache/v2 v2.1.3 // indirect
	github.com/kpango/glg v1.6.15 // indirect
	github.com/minuk-dev/opentelemetry-querier/acceptor v0.1.0 // indirect
	github.com/minuk-dev/opentelemetry-querier/dispatcher v0.1.0 // indirect
	github.com/minuk-dev/opentelemetry-querier/gen v0.1.0 // indirect
	github.com/minuk-dev/opentelemetry-querier/pipeline v0.1.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/samber/lo v1.53.0 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace github.com/minuk-dev/opentelemetry-querier/acceptor => ../../acceptor

replace github.com/minuk-dev/opentelemetry-querier/component => ../../component

replace github.com/minuk-dev/opentelemetry-querier/dispatcher => ../../dispatcher

replace github.com/minuk-dev/opentelemetry-querier/gen => ../../gen

replace github.com/minuk-dev/opentelemetry-querier/pipeline => ../../pipeline

replace github.com/minuk-dev/opentelemetry-querier/processor => ../../processor

replace github.com/minuk-dev/opentelemetry-querier/qdata => ../../qdata

replace github.com/minuk-dev/opentelemetry-querier/qerror => ../../qerror

replace github.com/minuk-dev/opentelemetry-querier/extension/athenzauthextension => ../../extension/athenzauthextension
