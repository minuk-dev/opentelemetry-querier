module github.com/minuk-dev/opentelemetry-querier/cmd/querier

go 1.25.4

require (
	github.com/minuk-dev/opentelemetry-querier/acceptor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/acceptor/otqp v0.0.0
	github.com/minuk-dev/opentelemetry-querier/acceptor/prometheusacceptor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/component v0.0.0
	github.com/minuk-dev/opentelemetry-querier/dispatcher v0.0.0
	github.com/minuk-dev/opentelemetry-querier/dispatcher/promdispatcher v0.0.0
	github.com/minuk-dev/opentelemetry-querier/processor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/processor/authratelimit v0.0.0
	github.com/minuk-dev/opentelemetry-querier/processor/queryrewrite v0.0.0
	github.com/minuk-dev/opentelemetry-querier/processor/responsefilter v0.0.0
	github.com/minuk-dev/opentelemetry-querier/processor/tenant v0.0.0
	github.com/minuk-dev/opentelemetry-querier/querier v0.0.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dennwc/varint v1.0.0 // indirect
	github.com/go-kit/log v0.2.1 // indirect
	github.com/go-logfmt/logfmt v0.6.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.2.1 // indirect
	github.com/grafana/regexp v0.0.0-20240518133315-a468a5bfb3bc // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/minuk-dev/opentelemetry-querier/gen v0.0.0 // indirect
	github.com/minuk-dev/opentelemetry-querier/pipeline v0.0.0 // indirect
	github.com/minuk-dev/opentelemetry-querier/qdata v0.0.0 // indirect
	github.com/minuk-dev/opentelemetry-querier/qerror v0.0.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_golang v1.19.1 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/common v0.55.0 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	github.com/prometheus/prometheus v0.54.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/net v0.27.0 // indirect
	golang.org/x/sys v0.22.0 // indirect
	golang.org/x/text v0.16.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240708141625-4ad9e859172b // indirect
	google.golang.org/grpc v1.66.0 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/minuk-dev/opentelemetry-querier/acceptor => ../../acceptor

replace github.com/minuk-dev/opentelemetry-querier/acceptor/otqp => ../../acceptor/otqp

replace github.com/minuk-dev/opentelemetry-querier/acceptor/prometheusacceptor => ../../acceptor/prometheusacceptor

replace github.com/minuk-dev/opentelemetry-querier/component => ../../component

replace github.com/minuk-dev/opentelemetry-querier/dispatcher => ../../dispatcher

replace github.com/minuk-dev/opentelemetry-querier/dispatcher/promdispatcher => ../../dispatcher/promdispatcher

replace github.com/minuk-dev/opentelemetry-querier/gen => ../../gen

replace github.com/minuk-dev/opentelemetry-querier/pipeline => ../../pipeline

replace github.com/minuk-dev/opentelemetry-querier/processor => ../../processor

replace github.com/minuk-dev/opentelemetry-querier/processor/authratelimit => ../../processor/authratelimit

replace github.com/minuk-dev/opentelemetry-querier/processor/queryrewrite => ../../processor/queryrewrite

replace github.com/minuk-dev/opentelemetry-querier/processor/responsefilter => ../../processor/responsefilter

replace github.com/minuk-dev/opentelemetry-querier/processor/tenant => ../../processor/tenant

replace github.com/minuk-dev/opentelemetry-querier/qdata => ../../qdata

replace github.com/minuk-dev/opentelemetry-querier/qerror => ../../qerror

replace github.com/minuk-dev/opentelemetry-querier/querier => ../../querier
