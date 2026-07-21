module github.com/minuk-dev/opentelemetry-querier/cmd/querier

go 1.25.4

require (
	github.com/minuk-dev/opentelemetry-querier/acceptor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/acceptor/lokiacceptor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/acceptor/otqpacceptor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/acceptor/prometheusacceptor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/component v0.0.0
	github.com/minuk-dev/opentelemetry-querier/dispatcher v0.0.0
	github.com/minuk-dev/opentelemetry-querier/dispatcher/prometheusdispatcher v0.0.0
	github.com/minuk-dev/opentelemetry-querier/processor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/processor/authratelimitprocessor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/processor/queryrewriteprocessor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/processor/responsefilterprocessor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/processor/simpleauthzprocessor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/processor/tenantprocessor v0.0.0
	github.com/minuk-dev/opentelemetry-querier/querier v0.0.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dennwc/varint v1.0.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/grafana/regexp v0.0.0-20250905093917-f7b3be9d1853 // indirect
	github.com/minuk-dev/opentelemetry-querier/gen v0.0.0 // indirect
	github.com/minuk-dev/opentelemetry-querier/pipeline v0.0.0 // indirect
	github.com/minuk-dev/opentelemetry-querier/qdata v0.0.0 // indirect
	github.com/minuk-dev/opentelemetry-querier/qerror v0.0.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_golang v1.23.2 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.69.0 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	github.com/prometheus/prometheus v0.313.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260610212136-7ab31c22f7ad // indirect
	google.golang.org/grpc v1.82.1 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/minuk-dev/opentelemetry-querier/acceptor => ../../acceptor

replace github.com/minuk-dev/opentelemetry-querier/acceptor/lokiacceptor => ../../acceptor/lokiacceptor

replace github.com/minuk-dev/opentelemetry-querier/acceptor/otqpacceptor => ../../acceptor/otqpacceptor

replace github.com/minuk-dev/opentelemetry-querier/acceptor/prometheusacceptor => ../../acceptor/prometheusacceptor

replace github.com/minuk-dev/opentelemetry-querier/component => ../../component

replace github.com/minuk-dev/opentelemetry-querier/dispatcher => ../../dispatcher

replace github.com/minuk-dev/opentelemetry-querier/dispatcher/prometheusdispatcher => ../../dispatcher/prometheusdispatcher

replace github.com/minuk-dev/opentelemetry-querier/gen => ../../gen

replace github.com/minuk-dev/opentelemetry-querier/pipeline => ../../pipeline

replace github.com/minuk-dev/opentelemetry-querier/processor => ../../processor

replace github.com/minuk-dev/opentelemetry-querier/processor/authratelimitprocessor => ../../processor/authratelimitprocessor

replace github.com/minuk-dev/opentelemetry-querier/processor/queryrewriteprocessor => ../../processor/queryrewriteprocessor

replace github.com/minuk-dev/opentelemetry-querier/processor/responsefilterprocessor => ../../processor/responsefilterprocessor

replace github.com/minuk-dev/opentelemetry-querier/processor/simpleauthzprocessor => ../../processor/simpleauthzprocessor

replace github.com/minuk-dev/opentelemetry-querier/processor/tenantprocessor => ../../processor/tenantprocessor

replace github.com/minuk-dev/opentelemetry-querier/qdata => ../../qdata

replace github.com/minuk-dev/opentelemetry-querier/qerror => ../../qerror

replace github.com/minuk-dev/opentelemetry-querier/querier => ../../querier
