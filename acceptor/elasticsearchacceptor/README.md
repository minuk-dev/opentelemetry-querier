# elasticsearchacceptor

Ingress acceptor that speaks the **Elasticsearch** `_search` API. It is the
counterpart of the
[elasticsearchdispatcher](../../dispatcher/elasticsearchdispatcher): clients that
already speak Elasticsearch can query through the proxy. Requests are parsed into
a qdata `Query` (Lucene dialect), run through the pipeline, and the qdata
`Result` is serialized back into the Elasticsearch `_search` response envelope.

- Serves `GET`/`POST` `/_search` against the dispatcher-configured index, plus a `/` ping. Index-specific paths are rejected.
- The query text comes from the `q` URL parameter or, for a `POST`, the JSON
  body's `query.query_string.query`; absent either, it defaults to match-all
  (`*`).
- A logs `Result` is rendered as `hits.hits[]`, each hit's `_source` carrying the
  record's `@timestamp`, `message`, and attributes.
- Errors use the Elasticsearch error envelope (`{"error":{...},"status":N}`) with
  the mapped HTTP status.

## Config

| Key | Default | Description |
| --- | --- | --- |
| `endpoint` | `0.0.0.0:9200` | HTTP listen address (the canonical Elasticsearch port). |

```yaml
acceptors:
  elasticsearch:
    endpoint: "0.0.0.0:9200"
```

This is a fixed-index API. Only `q` in the URL and `query` in the JSON body are supported. Explicit pagination, size, sort, and other unimplemented search options return HTTP 400; they are never silently discarded. Configure the backend index, size limit, and sorting policy on the dispatcher. Dynamic index authorization is not implemented.
