# builder

A small analogue of the opentelemetry-collector-builder (`ocb`). It reads a
[`builder.yaml`](../../builder.yaml) manifest listing the acceptor / processor /
dispatcher components to include, and generates the distribution's `main.go` and
`components.go` (factory registration) under `dist.output_path`.

This is the **build-time** layer of the two-layer model: `builder.yaml` selects
components; the generated distribution then reads a runtime `config.yaml` to
instantiate and wire them into pipelines.

```console
$ go run ./cmd/builder --config builder.yaml
builder: generated distribution "querier" in ./cmd/querier
```
