# prometheus dispatcher

Renders a `qdata.Query` to the **Prometheus HTTP query API**, executes it against
an upstream, and parses the JSON response back into a `qdata.Result`.

- Instant context → `POST /api/v1/query`; range context → `/api/v1/query_range`.
- The resolved tenant is forwarded via `tenant_header`.
- Series carry `METRIC_TYPE_UNSPECIFIED` (Prometheus is type-less; per the QLSWG
  spec this is UNKNOWN, not an assumed GAUGE).
- Upstream `warnings` are surfaced through the result's feedback channel.

## Config

| Key | Default | Description |
| --- | --- | --- |
| `endpoint` | `http://localhost:9090` | Upstream base URL. |
| `tenant_header` | `X-Scope-OrgID` | Header used to forward the tenant id. |
| `timeout` | `30s` | Per-request timeout. |

```yaml
dispatchers:
  prometheus:
    endpoint: "http://localhost:9090"
    timeout: 30s
```
