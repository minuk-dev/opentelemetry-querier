# authratelimit processor

The gateway processor: **bearer-token authentication** and **per-tenant rate
limiting**. It runs first on the request path and short-circuits with a coded
error (`Unauthenticated` / `ResourceExhausted`) so no unauthenticated or
over-quota query reaches storage.

Rate limiting uses a token-bucket keyed globally or per tenant.

## Bounded key space

Per-tenant keys are request-derived, so the bucket map is bounded on three axes
(issue #68):

- **`max_keys`** caps how many keys hold their own bucket. Buckets are ordered by
  recency, so the entry dropped to make room is always the one idle longest.
- **Admission.** Once the map is full, minting a bucket for an unseen key costs a
  token from a shared *admission* bucket. Eviction alone would not be enough: a
  re-created key starts full, so without this gate a caller cycling tenant ids
  would still be limited only by how fast the map turns over. Charging every mint
  to one bucket caps that churn at `requests_per_second`.
- **Key length.** A tenant id is a header value and nothing upstream bounds it,
  so `max_keys` alone caps the entry count, not the bytes each entry retains.
  Ids over 256 bytes are keyed by their SHA-256 digest, which keeps distinct
  tenants distinct without keeping the id alive.

A tenant that holds a bucket is unaffected: its own bucket, its own refill
schedule, and the churn can only evict as fast as it wins admission tokens.

### What this does not fix

`per_tenant` assumes the tenant id is **trusted** — resolved by the tenant
processor from `X-Scope-OrgID`, which an upstream gateway controls and the
acceptors strip from client-supplied metadata at ingress (issue #65). If the id
is attacker-chosen, a flood of fresh ids fills the key space and every
non-resident tenant falls back to the shared admission bucket, so per-tenant
isolation degrades to a single global bucket. That degradation is inherent: no
bounded key space can keep per-tenant state for an unbounded stream of
caller-chosen keys. What the bound does guarantee is that the flood costs
bounded memory and buys no more than the configured rate — it is a fairness
loss, not a capacity loss or an OOM.

## Config

| Key | Default | Description |
| --- | --- | --- |
| `require_bearer` | `false` | Require `Authorization: Bearer <token>`. |
| `tokens` | `[]` | Accepted bearer tokens. |
| `requests_per_second` | `0` | Sustained per-key query rate (0 disables limiting). |
| `burst` | `ceil(rps)` | Token-bucket capacity. |
| `per_tenant` | `false` | Key the limiter by tenant id instead of one global bucket. |
| `max_keys` | `10000` | Cap on keys holding their own bucket; unseen keys beyond it must win an admission token. |

```yaml
processors:
  authratelimit:
    require_bearer: true
    tokens: ["dev-token"]
    requests_per_second: 50
    burst: 100
    per_tenant: true
    max_keys: 10000
```
