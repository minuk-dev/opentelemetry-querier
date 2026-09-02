# lokidispatcher

Storage-facing dispatcher for **Grafana Loki**. It renders a qdata `Query` to the
Loki HTTP query API, executes it against an upstream, and parses the JSON
response back into a qdata `Result`. It is the logs counterpart of the
[prometheusdispatcher](../prometheusdispatcher) (metrics).

- Accepts only the **`logql`** dialect; any other dialect is rejected with
  `InvalidArgument` before a request is built (the dispatcher half of the dialect
  contract).
- Instant queries hit `/loki/api/v1/query`; range queries (`ContextRange`) hit
  `/loki/api/v1/query_range`.
- Loki `streams` results become a **logs** payload (one `LogRecord` per entry,
  stream labels become record attributes). `matrix`/`vector` results from LogQL
  metric queries (e.g. `rate(...)`) become a **metrics** payload, mirroring the
  Prometheus dispatcher.
- The resolved tenant id is forwarded via `X-Scope-OrgID` (configurable).

## Rendering constraints

The plan is rendered to LogQL *text*, so every identifier it interpolates —
stream label names, `by` / `without` label lists, and function names — must be a
bare LogQL identifier (`[a-zA-Z_][a-zA-Z0-9_]*`). Anything else is rejected with
`CodeInvalidArgument` before the upstream is contacted, because an identifier
carrying LogQL syntax could close the construct it sits in and append a second
stream selector that the enforced tenant matchers never reach (issue #64).
Matcher *values* and line filters are unaffected — they are quoted.

Note this rejects dotted OpenTelemetry attribute names (`http.status_code`),
which have no unquoted LogQL spelling.

## Config

| Key | Default | Description |
| --- | --- | --- |
| `endpoint` | `http://localhost:3100` | Upstream Loki base URL. |
| `tenant_header` | `X-Scope-OrgID` | Header used to forward the resolved tenant id. |
| `timeout` | `30s` | Bounds each upstream request. |
| `limit` | `100` | Caps the number of log entries returned. |
| `direction` | `backward` | Scan direction: `forward` or `backward`. |

```yaml
dispatchers:
  loki:
    endpoint: "http://localhost:3100"
    limit: 500
    direction: "backward"
```
