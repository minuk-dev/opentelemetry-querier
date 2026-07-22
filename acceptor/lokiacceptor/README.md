# lokiacceptor

Ingress acceptor that speaks the **Grafana Loki** HTTP query API. It is the
counterpart of the [lokidispatcher](../../dispatcher/lokidispatcher): clients
that already speak Loki can query through the proxy. Requests are parsed into a
qdata `Query` (LogQL dialect), run through the pipeline, and the qdata `Result`
is serialized back into the Loki JSON response envelope.

- Serves `/loki/api/v1/query` (instant) and `/loki/api/v1/query_range` (range),
  plus `/ready`.
- Parses `query`, `time`/`start`/`end` (Unix nanoseconds, Unix-seconds float, or
  RFC3339), and `step` (LogQL metric queries only).
- A logs `Result` is rendered as Loki **`streams`** (records are grouped by label
  set into streams); a metrics `Result` is rendered as a **`matrix`**.
- Errors are returned as plain text with the mapped HTTP status, matching Loki.

## Config

| Key | Default | Description |
| --- | --- | --- |
| `endpoint` | `0.0.0.0:3100` | HTTP listen address (the canonical Loki port). |

```yaml
acceptors:
  loki:
    endpoint: "0.0.0.0:3100"
```
