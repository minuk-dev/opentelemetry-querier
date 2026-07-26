// Package e2e holds Docker-backed end-to-end tests, guarded by the "e2e" build
// tag: they spin up real Prometheus / Loki / Elasticsearch backends via
// testcontainers, seed data, and run actual queries through the acceptor ->
// pipeline -> dispatcher path. They are excluded from the default build/test and
// require a running Docker daemon.
//
// Run them with:
//
//	go test -tags=e2e ./test/e2e/...
package e2e
