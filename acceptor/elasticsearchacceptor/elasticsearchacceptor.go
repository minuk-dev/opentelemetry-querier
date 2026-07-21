// Package elasticsearchacceptor implements an acceptor that speaks the
// Elasticsearch _search API. It is the ingress counterpart of the
// elasticsearchdispatcher: clients that already speak Elasticsearch can query
// through the proxy. Requests are parsed into a qdata Query (Lucene dialect),
// run through the pipeline, and the qdata Result is serialized back into the
// Elasticsearch _search response envelope.
package elasticsearchacceptor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// DefaultEndpoint is the default HTTP listen address (the canonical
// Elasticsearch port).
const DefaultEndpoint = "0.0.0.0:9200"

// matchAll is the Lucene query that selects every document, used when the
// request carries no query text.
const matchAll = "*"

const (
	readHeaderTimeout = 10 * time.Second
)

var errBadBody = errors.New("elasticsearchacceptor: invalid request body")

// Config configures the Elasticsearch acceptor.
type Config struct {
	// Endpoint is the HTTP listen address.
	Endpoint string `mapstructure:"endpoint"`
}

// Acceptor serves the Elasticsearch _search API.
type Acceptor struct {
	cfg     Config
	handler pipeline.Handler
	server  *http.Server
}

// New builds an Elasticsearch acceptor bound to the given pipeline Handler.
func New(cfg Config, handler pipeline.Handler) *Acceptor {
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}

	return &Acceptor{cfg: cfg, handler: handler, server: nil}
}

// Handler returns the HTTP handler serving the query API. It is exposed so the
// acceptor can be embedded or tested with httptest.
func (a *Acceptor) Handler() http.Handler {
	mux := http.NewServeMux()
	// _search under any index (or index pattern) path prefix, plus the bare form.
	mux.HandleFunc("/_search", a.handleSearch)
	mux.HandleFunc("/", a.route)

	return mux
}

// Start binds the listener and serves in the background.
func (a *Acceptor) Start(ctx context.Context, _ component.Host) error {
	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(ctx, "tcp", a.cfg.Endpoint)
	if err != nil {
		return fmt.Errorf("elasticsearchacceptor: listen %s: %w", a.cfg.Endpoint, err)
	}

	a.server = &http.Server{
		Addr:              a.cfg.Endpoint,
		Handler:           a.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	go func() { _ = a.server.Serve(listener) }()

	return nil
}

// Shutdown gracefully stops the server.
func (a *Acceptor) Shutdown(ctx context.Context) error {
	if a.server == nil {
		return nil
	}

	err := a.server.Shutdown(ctx)
	if err != nil {
		return fmt.Errorf("elasticsearchacceptor: shutdown: %w", err)
	}

	return nil
}

// route dispatches _search requests under an index path (e.g. /logs-*/_search)
// and answers the ping endpoint ("/"); anything else is a 404.
func (a *Acceptor) route(writer http.ResponseWriter, request *http.Request) {
	if strings.HasSuffix(request.URL.Path, "/_search") {
		a.handleSearch(writer, request)

		return
	}

	if request.URL.Path == "/" {
		writeJSON(writer, http.StatusOK, map[string]any{"tagline": "You Know, for Search"})

		return
	}

	http.NotFound(writer, request)
}

func (a *Acceptor) handleSearch(writer http.ResponseWriter, request *http.Request) {
	query, err := parseSearch(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err)

		return
	}

	result, err := a.handler.Handle(request.Context(), query)
	if err != nil {
		writeHandlerError(writer, err)

		return
	}

	writeJSON(writer, http.StatusOK, resultToResponse(result))
}

// ---- request parsing ----

// parseSearch builds a Lucene qdata Query from the _search request. The query
// text comes from the `q` URL parameter or, for a POST, the JSON body's
// query_string; absent either, it defaults to match-all.
func parseSearch(request *http.Request) (*qdata.Query, error) {
	err := request.ParseForm()
	if err != nil {
		return nil, fmt.Errorf("elasticsearchacceptor: parse form: %w", err)
	}

	expr := request.Form.Get("q")

	if expr == "" && request.Body != nil {
		bodyExpr, bodyErr := queryStringFromBody(request.Body)
		if bodyErr != nil {
			return nil, bodyErr
		}

		expr = bodyExpr
	}

	if expr == "" {
		expr = matchAll
	}

	query := &qdata.Query{
		Signal:  qdata.SignalLogs,
		Context: qdata.ContextInstant,
		Expr:    expr,
		Dialect: qdata.DialectLucene,
	}
	injectHeaders(query, request.Header)

	return query, nil
}

// queryStringFromBody extracts query.query_string.query from a JSON _search
// body. Elasticsearch's field names use snake_case that no struct tag can
// express in camelCase, so the body is walked as a generic map. An empty body
// (io.EOF) yields the empty string, i.e. match-all.
func queryStringFromBody(reader io.Reader) (string, error) {
	var body map[string]any

	err := json.NewDecoder(reader).Decode(&body)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return "", nil
		}

		return "", fmt.Errorf("%w: %w", errBadBody, err)
	}

	query, ok := body["query"].(map[string]any)
	if !ok {
		return "", nil
	}

	queryString, ok := query["query_string"].(map[string]any)
	if !ok {
		return "", nil
	}

	text, ok := queryString["query"].(string)
	if !ok {
		return "", nil
	}

	return text, nil
}

func injectHeaders(query *qdata.Query, header http.Header) {
	if len(header) == 0 {
		return
	}

	if query.Header == nil {
		query.Header = make(map[string]*qdata.HeaderValues, len(header))
	}

	for key, values := range header {
		query.Header[key] = &qdata.HeaderValues{Values: values}
	}
}

// ---- response serialization ----

// resultToResponse renders a qdata logs Result as an Elasticsearch _search
// response: each record becomes a hit whose _source carries the timestamp,
// message and attributes. The envelope is built as a generic map because
// Elasticsearch's field names (timed_out, _index, _source, ...) are snake_case
// or underscore-prefixed and cannot be expressed as camelCase struct tags.
func resultToResponse(result *qdata.Result) map[string]any {
	records := result.GetLogs().GetRecords()
	hits := make([]map[string]any, 0, len(records))

	for _, record := range records {
		hits = append(hits, recordToHit(record))
	}

	return map[string]any{
		"took":      0,
		"timed_out": false,
		"hits": map[string]any{
			"total": map[string]any{"value": len(hits), "relation": "eq"},
			"hits":  hits,
		},
	}
}

func recordToHit(record *qdata.LogRecord) map[string]any {
	source := map[string]any{}

	for _, attr := range record.GetAttributes().GetValues() {
		source[attr.GetKey()] = qdata.ValueString(attr.GetValue())
	}

	source["@timestamp"] = record.GetEnd().AsTime().UTC().Format(time.RFC3339Nano)
	source["message"] = qdata.ValueString(record.GetBody())

	index := stringOr(source["_index"], "querier")
	id := stringOr(source["_id"], "")
	delete(source, "_index")
	delete(source, "_id")

	return map[string]any{"_index": index, "_id": id, "_source": source}
}

// stringOr returns value as a string, or fallback when it is absent or empty.
func stringOr(value any, fallback string) string {
	text, ok := value.(string)
	if !ok || text == "" {
		return fallback
	}

	return text
}

func writeJSON(writer http.ResponseWriter, status int, payload any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)

	// Headers are already sent, so an encode error can only be logged/dropped.
	err := json.NewEncoder(writer).Encode(payload)
	if err != nil {
		return
	}
}

// writeError renders an Elasticsearch-style error envelope.
func writeError(writer http.ResponseWriter, status int, err error) {
	writeJSON(writer, status, map[string]any{
		"error":  map[string]any{"type": "query_error", "reason": err.Error()},
		"status": status,
	})
}

func writeHandlerError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError

	var coded *qerror.Error
	if errors.As(err, &coded) {
		status = coded.HTTPStatus()
	}

	writeError(writer, status, err)
}
