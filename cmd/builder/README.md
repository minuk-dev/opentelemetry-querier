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

## Standalone distribution

Starting with the coordinated `v0.1.0` module release, install the builder at
a specific version with Go 1.25.4 or newer. Run these commands in your own
project, with the two files from [the standalone example](../../examples/standalone):

```sh
go install github.com/minuk-dev/opentelemetry-querier/cmd/builder@v0.1.0
builder --build --config builder.yaml
./dist/custom-querier --config config.yaml
```

The example routes Prometheus queries received on port 9095 to an upstream
Prometheus on port 9090. Set the upstream endpoint in `config.yaml`.

- `dist.module` names the generated application module. `dist.version` is the
  application version. `dist.core_version` selects the Querier framework modules.
- List each component by its module path and version, for example
  `gomod: example.com/team/myprocessor@v0.1.0`. The older space-separated form
  also works. The package at that module path must expose `NewFactory()`.
- Go selects dependencies from the resulting requirements. Inspect the chosen
  versions with `GOWORK=off go list -m all` in the output directory.
- `dist.output_path` is relative to the directory where you run the builder,
  unless it is absolute. Use a dedicated output directory. Rebuilds can update
  the generated files for the same application module and binary name. A
  dependency or compiler failure leaves the previous build in place.

Omit `--build` for source generation in the repository workspace. Existing
manifests can keep unversioned entries in that mode.

The release workflow installs the published builder outside the source tree,
builds the standalone example, and checks a Prometheus query against a stub
backend with the resulting binary.
