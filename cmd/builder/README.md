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

## Go module releases

The first coordinated Go module release is `v0.1.0`. Publish the root
`v0.1.0` tag on the commit containing the module dependency updates. The
release workflow creates tags such as `querier/v0.1.0`,
`qdata/v0.1.0`, and `cmd/builder/v0.1.0` on the same commit and pushes
them together. The root tag also versions the default binary.

Internal requirements in published modules refer to released versions.
The workspace still selects local modules for development. For an
incompatible change spanning several modules, update the affected
requirements together and publish their tags from the same commit.
Check the candidate from outside the workspace before cutting its tag.
