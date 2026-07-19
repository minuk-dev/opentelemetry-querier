# otqp acceptor

The default acceptor: **OTQP (OpenTelemetry Query Protocol)**, served over both
gRPC and HTTP. It is the query-side analogue of an OTLP receiver.

A `QueryRequest` carries a `qdata.Query`; the acceptor runs it through the
pipeline and returns a `QueryResponse` carrying the `qdata.Result` (with its
feedback side channel).

- **gRPC** — `QueryService/Query` and `QueryService/QueryStream` (default `:4327`).
- **HTTP** — `POST /v1/query` with a protobuf (`application/x-protobuf`) or JSON
  (`application/json`) body (default `:4328`), plus `GET /healthz`.

Inbound request credentials are copied onto the query so downstream processors
(auth, tenant) can read them: HTTP headers on the HTTP path, and gRPC metadata on
the gRPC path (metadata keys are lower-cased by gRPC, but the processors look
them up case-insensitively). A gRPC client therefore sends `X-Scope-OrgID` etc.
as request metadata, not in the query body.

## Config

| Key | Default | Description |
| --- | --- | --- |
| `grpc_endpoint` | `0.0.0.0:4327` | gRPC listen address (empty disables gRPC). |
| `http_endpoint` | `0.0.0.0:4328` | HTTP listen address (empty disables HTTP). |

```yaml
acceptors:
  otqp:
    grpc_endpoint: "0.0.0.0:4327"
    http_endpoint: "0.0.0.0:4328"
```
