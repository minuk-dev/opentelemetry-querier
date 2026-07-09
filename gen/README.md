# gen

Generated Go code — **do not edit by hand**.

`buf generate` (see [`buf.gen.yaml`](../buf.gen.yaml)) produces this module from
the protobuf sources under [`proto/`](../proto):

- `gen/qdata/v1` — the standardized query & result model ([qdata](../qdata)).
- `gen/otqp/v1` — the OTQP service (`QueryService`) and its request/response
  envelopes.

Regenerate with:

```console
$ buf generate
```
