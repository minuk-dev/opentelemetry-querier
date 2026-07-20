# simpleauthz processor

Simple per-subject authorization. It resolves the **subject** (the user a query
runs as) and applies an ordered policy that can **reject** the query or **scope**
it down.

- **Reject** — a matched `deny` rule (or an unmatched subject under a `deny`
  default) short-circuits the pipeline with a `PermissionDenied` error (HTTP
  403), so the query never reaches storage.
- **Filter** — a matched `allow` rule registers enforced label matchers, which
  downstream [queryrewrite](../queryrewriteprocessor) weaves into the query expression —
  the same mechanism the [tenant](../tenantprocessor) processor uses for isolation. A
  subject can therefore only see the series its rule permits.

The subject is read from a request header (`X-Scope-User` by default), or from
the resolved tenant id with `from_tenant`. Place this processor **after**
`tenant` (so `from_tenant` sees the tenant) and **before** `queryrewrite` (so the
injected matchers are enforced).

## Fail closed

`default_effect` applies when no rule matches and defaults to **`deny`**. An
empty policy therefore denies every query. Set `default_effect: allow` for an
allowlist-of-denies posture instead. An unknown effect string is rejected at
startup rather than silently allowing a query.

## Config

| Key | Default | Description |
| --- | --- | --- |
| `subject_header` | `X-Scope-User` | Request header carrying the subject id. |
| `from_tenant` | `false` | Resolve the subject from the tenant id instead of the header. |
| `default_subject` | `""` | Subject used when neither header nor tenant resolves one. |
| `required` | `false` | Reject queries for which no subject resolves (`PermissionDenied`). |
| `default_effect` | `deny` | Effect applied when no rule matches: `allow` or `deny`. |
| `rules` | `[]` | Ordered policy; the first matching rule wins. |

Each **rule**:

| Key | Default | Description |
| --- | --- | --- |
| `subjects` | `[]` | Subject ids this rule applies to. Empty is a catch-all (any subject). |
| `effect` | `allow` | `allow` or `deny`. |
| `enforce_labels` | `[]` | Matchers injected when the rule allows (see below). |

Each **enforce_label**: `name` (required), `value` (static), `from_tenant`
(use the resolved tenant id as the value). A label whose resolved value is empty
is skipped rather than enforcing `name=""`.

```yaml
processors:
  simpleauthz:
    subject_header: "X-Scope-User"
    default_effect: "deny"
    rules:
      # admins query anything, unscoped.
      - subjects: ["admin"]
        effect: "allow"
      # alice is allowed, but only within her namespace.
      - subjects: ["alice"]
        effect: "allow"
        enforce_labels:
          - name: "namespace"
            value: "team-a"
      # everyone else with a resolved subject: scope to their own tenant.
      - effect: "allow"
        enforce_labels:
          - name: "tenant_id"
            from_tenant: true
```

## Limitations

Scoping relies on `queryrewrite`, which injects flat equality matchers into
PromQL today. For a dialect with no injector the enforced matchers cannot be
applied and `queryrewrite` fails closed. `enforce_labels` express an AND of
equality constraints; richer boolean enforcement is the `enforced_predicates`
path (see [the design note](../../docs/design/qdata-cross-language-query.md)).
