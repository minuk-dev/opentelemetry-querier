# authratelimit processor

The gateway processor: **bearer-token authentication** and **per-tenant rate
limiting**. It runs first on the request path and short-circuits with a coded
error (`Unauthenticated` / `ResourceExhausted`) so no unauthenticated or
over-quota query reaches storage.

Rate limiting uses a token-bucket keyed globally or per tenant.

## Config

| Key | Default | Description |
| --- | --- | --- |
| `require_bearer` | `false` | Require `Authorization: Bearer <token>`. |
| `tokens` | `[]` | Accepted bearer tokens. |
| `requests_per_second` | `0` | Sustained per-key query rate (0 disables limiting). |
| `burst` | `ceil(rps)` | Token-bucket capacity. |
| `per_tenant` | `false` | Key the limiter by tenant id instead of one global bucket. |

```yaml
processors:
  authratelimit:
    require_bearer: true
    tokens: ["dev-token"]
    requests_per_second: 50
    burst: 100
    per_tenant: true
```
