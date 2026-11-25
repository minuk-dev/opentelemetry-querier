# cacheprocessor

Add a named `cache` instance at the desired position in a leaf pipeline's
`processors` list. For example, this pipeline caches the result after
`responsefilter` has masked it.

```yaml
processors:
  cache/queries:
    ttl: 30s
    max_bytes: 16777216
    key: '{{ with index .Header "X-View" }}{{ index .Values 0 }}{{ end }}'

service:
  pipelines:
    metrics/default:
      acceptors: [otqp]
      processors: [tenant, authratelimit, simpleauthz, queryrewrite, cache/queries, responsefilter]
      dispatchers: [prometheus]
```

All request hooks run in list order on every request. On a miss, the dispatcher
runs, followed by the response hooks after `cache` in reverse order. The cache
stores the result at that boundary. Response hooks before `cache` run in reverse
order for every caller, including hits. Place response hooks that use per-request
state before the cache. For cross-signal queries, configure the cache in the
connector's leaf pipelines so each target runs its own authentication and policy.

Both `ttl` and `max_bytes` must be positive. The byte budget counts encoded result
payloads and their keys. The least recently used entries are evicted to make
room. Configure a trusted tenant source. Requests without a resolved tenant,
queries evaluated at the implicit current time, and streaming queries bypass the
cache. Oversized results, failures, and known partial search results are skipped.

By default, the key hashes the rewritten query, including metadata and headers.
Set `key` to a Go text template when you want to choose which additional header
values vary results. The template reads the effective `qdata.Query`, including
`.Header` and `.Metadata`. The key always includes the rewritten plan, tenant
and policy metadata, time, options, and Authorization and Cookie headers.
Include any other headers that affect the backend response or a response hook
after the cache. Bad template syntax fails configuration. Template evaluation
errors bypass the cache for that request.

Each processor instance belongs to one pipeline and replica. Concurrent misses
at the same key share one downstream execution. Each caller keeps its own
deadline, and the last caller to leave cancels unfinished work.

### Verified identity isolation

On authenticated pipelines, set `require_verified_identity: true` and put
the cache last in the processor list. Requests without verified identity
bypass the cache. The cache is local to its pipeline instance and keys
the rewritten effective query, verified issuer and principal, selected
tenant, and verified groups, roles and scopes. Authorization runs before
each lookup and every result processor runs for hits and misses. A policy
change that rewrites the query changes the key. Implicit current-time
queries continue to bypass the cache.
