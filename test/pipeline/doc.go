// Package pipeline holds end-to-end tests that progressively add each processor
// to a running pipeline and assert its observable effect through the full
// acceptor -> pipeline -> dispatcher path (issue #58).
//
// Unlike the Docker-backed tests in test/e2e, these need no external backend:
// they drive a native Prometheus acceptor via its Handler() and terminate the
// pipeline with a real Prometheus dispatcher pointed at an httptest upstream
// that records the rendered query and returns canned responses. Each test adds
// one more processor to the chain and asserts the incremental behavior, so the
// full default chain (authratelimit -> tenant -> simpleauthz -> queryrewrite ->
// responsefilter, as in config.yaml) is validated cumulatively.
//
// They run by default:
//
//	go test ./test/pipeline/...
package pipeline
