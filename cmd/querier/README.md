# querier (distribution)

The default OpenTelemetry Querier distribution.

`main.go` and `components.go` are **generated** by [cmd/builder](../builder) from
[`builder.yaml`](../../builder.yaml) — do not edit them by hand. `components.go`
registers the compiled-in factories; `main.go` loads a runtime config, assembles
the pipelines, and runs them.

```console
$ go build -o bin/querier ./cmd/querier
$ ./bin/querier --config config.yaml
```

To change which components are compiled in, edit `builder.yaml` and re-run the
builder.
