# elasticsearchdispatcher

Storage-facing dispatcher for **Elasticsearch**. It renders a qdata `Query` to
the Elasticsearch `_search` API, executes it against an upstream, and parses the
JSON response back into a qdata **logs** `Result`. It sits alongside the
[lokidispatcher](../lokidispatcher) (Loki logs) and the
[prometheusdispatcher](../prometheusdispatcher) (metrics).

- Accepts only the **`lucene`** dialect; any other dialect is rejected with
  `InvalidArgument` before a request is built (the dispatcher half of the dialect
  contract).
- The query `expr` is sent as a `query_string`, wrapped in a `bool.must` with a
  `range` filter on the configured time field derived from the query's time
  range, sorted newest-first and capped at `size`.
- Each hit becomes a `LogRecord`: the timestamp comes from the `time_field`, the
  body from a `message` field (falling back to the raw `_source`), and every
  `_source` field (plus `_index`/`_id`) becomes an attribute.
- Optional HTTP basic auth via `username`/`password`.

## Config

| Key | Default | Description |
| --- | --- | --- |
| `endpoint` | `http://localhost:9200` | Upstream Elasticsearch base URL. |
| `index` | `_all` | Index or index pattern to search. |
| `time_field` | `@timestamp` | Document field carrying the record timestamp. |
| `size` | `100` | Caps the number of hits returned. |
| `timeout` | `30s` | Bounds each upstream request. |
| `username` | `""` | HTTP basic auth user (auth sent only when set). |
| `password` | `""` | HTTP basic auth password. |

```yaml
dispatchers:
  elasticsearch:
    endpoint: "http://localhost:9200"
    index: "logs-*"
    time_field: "@timestamp"
    size: 500
```
