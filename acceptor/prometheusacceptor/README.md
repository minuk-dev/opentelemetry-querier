# prometheus acceptor

An acceptor that speaks the **Prometheus HTTP query API** — the ingress
counterpart of the [prometheus dispatcher](../../dispatcher/promdispatcher).
Clients that already speak Prometheus can query through the proxy unchanged.

- `GET`/`POST /api/v1/query` — instant query (returns a `vector`).
- `GET`/`POST /api/v1/query_range` — range query (returns a `matrix`).
- `GET /healthz`.

A request is parsed into a `qdata.Query` (`dialect: promql`, instant/range
context, time range + step), run through the pipeline, and the `qdata.Result` is
serialized back into the Prometheus JSON envelope
(`{"status":"success","data":{"resultType":…,"result":[…]},"warnings":[…]}`).
Result feedback notifications are surfaced as `warnings`. Inbound HTTP headers
are copied onto the query so downstream processors (auth, tenant) can read them.

## Config

| Key | Default | Description |
| --- | --- | --- |
| `endpoint` | `0.0.0.0:9090` | HTTP listen address (override when a real Prometheus already uses 9090). |

```yaml
acceptors:
  prometheus:
    endpoint: "0.0.0.0:9090"
```
