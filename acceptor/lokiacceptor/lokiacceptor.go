// Package lokiacceptor implements an acceptor that speaks the Grafana Loki HTTP
// query API. It is the ingress counterpart of the lokidispatcher: clients that
// already speak Loki (`/loki/api/v1/query`, `/loki/api/v1/query_range`) can query
// through the proxy. Requests are parsed into a qdata Query (LogQL dialect), run
// through the pipeline, and the qdata Result is serialized back into the Loki
// JSON response envelope.
package lokiacceptor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// DefaultEndpoint is the default HTTP listen address (the canonical Loki port).
const DefaultEndpoint = "0.0.0.0:3100"

const (
	readHeaderTimeout = 10 * time.Second
	nanosPerSecond    = 1e9
	floatBitSize      = 64
	fullPrecision     = -1
	intBase           = 10
	intBitSize        = 64
	// secondsDigits is the integer-timestamp width at or below which Loki reads a
	// bare number as Unix seconds rather than nanoseconds.
	secondsDigits = 10
)

var (
	errMissingQuery = errors.New("lokiacceptor: missing 'query' parameter")
	errBadTime      = errors.New("lokiacceptor: invalid time parameter")
	errBadStep      = errors.New("lokiacceptor: invalid 'step' parameter")
)

// Config configures the Loki acceptor.
type Config struct {
	// Endpoint is the HTTP listen address.
	Endpoint string `mapstructure:"endpoint"`
}

// Acceptor serves the Loki HTTP query API.
type Acceptor struct {
	cfg     Config
	handler pipeline.Handler
	server  *http.Server
}

// New builds a Loki acceptor bound to the given pipeline Handler.
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
	mux.HandleFunc("/loki/api/v1/query", a.handleInstant)
	mux.HandleFunc("/loki/api/v1/query_range", a.handleRange)
	mux.HandleFunc("/ready", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})

	return mux
}

// Start binds the listener and serves in the background.
func (a *Acceptor) Start(ctx context.Context, _ component.Host) error {
	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(ctx, "tcp", a.cfg.Endpoint)
	if err != nil {
		return fmt.Errorf("lokiacceptor: listen %s: %w", a.cfg.Endpoint, err)
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
		return fmt.Errorf("lokiacceptor: shutdown: %w", err)
	}

	return nil
}

func (a *Acceptor) handleInstant(writer http.ResponseWriter, request *http.Request) {
	query, err := parseInstant(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err)

		return
	}

	a.serve(writer, request, query)
}

func (a *Acceptor) handleRange(writer http.ResponseWriter, request *http.Request) {
	query, err := parseRange(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, err)

		return
	}

	a.serve(writer, request, query)
}

func (a *Acceptor) serve(writer http.ResponseWriter, request *http.Request, query *qdata.Query) {
	result, err := a.handler.Handle(request.Context(), query)
	if err != nil {
		writeHandlerError(writer, err)

		return
	}

	writeJSON(writer, http.StatusOK, resultToResponse(result, query.GetContext() == qdata.ContextInstant))
}

// ---- request parsing ----

func parseInstant(request *http.Request) (*qdata.Query, error) {
	err := request.ParseForm()
	if err != nil {
		return nil, fmt.Errorf("lokiacceptor: parse form: %w", err)
	}

	expr := request.Form.Get("query")
	if expr == "" {
		return nil, errMissingQuery
	}

	evalAt := time.Now()

	if raw := request.Form.Get("time"); raw != "" {
		evalAt, err = parseTime(raw)
		if err != nil {
			return nil, err
		}
	}

	plan, err := parseLogQL(expr)
	if err != nil {
		return nil, err
	}

	query := &qdata.Query{Signal: qdata.SignalLogs, Context: qdata.ContextInstant, Plan: plan}
	query.Range = &qdata.TimeRange{Start: nil, End: timestamppb.New(evalAt), StartExclusive: false, EndExclusive: false}
	injectHeaders(query, request.Header)

	return query, nil
}

func parseRange(request *http.Request) (*qdata.Query, error) {
	err := request.ParseForm()
	if err != nil {
		return nil, fmt.Errorf("lokiacceptor: parse form: %w", err)
	}

	expr := request.Form.Get("query")
	if expr == "" {
		return nil, errMissingQuery
	}

	start, err := parseTime(request.Form.Get("start"))
	if err != nil {
		return nil, err
	}

	end, err := parseTime(request.Form.Get("end"))
	if err != nil {
		return nil, err
	}

	plan, err := parseLogQL(expr)
	if err != nil {
		return nil, err
	}

	query := &qdata.Query{Signal: qdata.SignalLogs, Context: qdata.ContextRange, Plan: plan}
	query.Range = &qdata.TimeRange{
		Start:          timestamppb.New(start),
		End:            timestamppb.New(end),
		StartExclusive: false,
		EndExclusive:   false,
	}

	// step is only meaningful for LogQL metric queries; carry it when present.
	if raw := request.Form.Get("step"); raw != "" {
		step, stepErr := parseStep(raw)
		if stepErr != nil {
			return nil, stepErr
		}

		query.Step = durationpb.New(step)
	}

	injectHeaders(query, request.Header)

	return query, nil
}

// parseTime accepts the forms the Loki query API accepts, matching Loki's own
// disambiguation (loghttp.parseTimestamp): a fractional value is Unix seconds; a
// bare integer of at most secondsDigits digits is Unix seconds, otherwise Unix
// nanoseconds; anything else is an RFC3339 timestamp.
func parseTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, fmt.Errorf("%w: empty", errBadTime)
	}

	// A fractional value is seconds since epoch (with fractional nanoseconds).
	if strings.Contains(raw, ".") {
		seconds, err := strconv.ParseFloat(raw, floatBitSize)
		if err == nil {
			whole, frac := math.Modf(seconds)

			return time.Unix(int64(whole), int64(frac*nanosPerSecond)), nil
		}
	}

	// A bare integer: Loki reads <=10 digits as Unix seconds, longer as nanos, so
	// a second-precision timestamp like 1700000000 is not misread as nanoseconds.
	digits, err := strconv.ParseInt(raw, intBase, intBitSize)
	if err == nil {
		if len(strings.TrimPrefix(raw, "-")) <= secondsDigits {
			return time.Unix(digits, 0), nil
		}

		return time.Unix(0, digits), nil
	}

	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %q", errBadTime, raw)
	}

	return parsed, nil
}

func parseStep(raw string) (time.Duration, error) {
	seconds, err := strconv.ParseFloat(raw, floatBitSize)
	if err == nil {
		return time.Duration(seconds * float64(time.Second)), nil
	}

	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", errBadStep, raw)
	}

	return parsed, nil
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

type lokiResponse struct {
	Status string   `json:"status"`
	Data   lokiData `json:"data"`
}

type lokiData struct {
	ResultType string      `json:"resultType"`
	Result     []lokiEntry `json:"result"`
}

// lokiEntry is one stream (Values holds [ts, line] pairs), one matrix series
// (Values holds [ts, value] sample pairs), or one vector series (Value holds a
// single [ts, value] sample). Only one shape is used per response.
type lokiEntry struct {
	Stream map[string]string `json:"stream,omitempty"`
	Metric map[string]string `json:"metric,omitempty"`
	Value  []any             `json:"value,omitempty"`
	Values [][]any           `json:"values,omitempty"`
}

// resultToResponse renders a qdata Result as a Loki response: logs become
// streams; range metrics become a matrix; instant metrics become a vector (a
// single value per series), matching Loki's own instant-vs-range result types.
func resultToResponse(result *qdata.Result, instant bool) lokiResponse {
	if metrics := result.GetMetrics(); metrics != nil && len(metrics.GetSeries()) > 0 {
		if instant {
			return lokiResponse{Status: "success", Data: lokiData{ResultType: "vector", Result: metricsToVector(metrics)}}
		}

		return lokiResponse{Status: "success", Data: lokiData{ResultType: "matrix", Result: metricsToMatrix(metrics)}}
	}

	return lokiResponse{Status: "success", Data: lokiData{ResultType: "streams", Result: logsToStreams(result.GetLogs())}}
}

// logsToStreams groups log records by their label set into Loki streams.
func logsToStreams(logs *qdata.Logs) []lokiEntry {
	order := make([]string, 0)
	streams := map[string]*lokiEntry{}

	for _, record := range logs.GetRecords() {
		labels := recordLabels(record)
		key := labelKey(labels)

		stream, ok := streams[key]
		if !ok {
			stream = &lokiEntry{Stream: labels, Metric: nil, Value: nil, Values: nil}
			streams[key] = stream
			order = append(order, key)
		}

		nanos := record.GetEnd().AsTime().UnixNano()
		line := qdata.ValueString(record.GetBody())
		stream.Values = append(stream.Values, []any{strconv.FormatInt(nanos, intBase), line})
	}

	out := make([]lokiEntry, 0, len(order))
	for _, key := range order {
		out = append(out, *streams[key])
	}

	return out
}

func recordLabels(record *qdata.LogRecord) map[string]string {
	labels := map[string]string{}
	for _, attr := range record.GetAttributes().GetValues() {
		labels[attr.GetKey()] = qdata.ValueString(attr.GetValue())
	}

	return labels
}

// labelKey builds a stable key from a label set so records with identical labels
// collapse into one stream.
func labelKey(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(labels[key])
		builder.WriteByte(',')
	}

	return builder.String()
}

// metricsToVector renders each series as a single [ts, value] sample (its last
// point), the shape Loki uses for an instant metric query.
func metricsToVector(metrics *qdata.Metrics) []lokiEntry {
	out := make([]lokiEntry, 0, len(metrics.GetSeries()))

	for _, series := range metrics.GetSeries() {
		entry := lokiEntry{Stream: nil, Metric: seriesLabels(series), Value: nil, Values: nil}

		points := series.GetPoints()
		if len(points) > 0 {
			last := points[len(points)-1]
			seconds := float64(last.GetEnd().AsTime().UnixNano()) / nanosPerSecond
			value := strconv.FormatFloat(last.GetValue().GetDoubleValue(), 'f', fullPrecision, floatBitSize)
			entry.Value = []any{seconds, value}
		}

		out = append(out, entry)
	}

	return out
}

func metricsToMatrix(metrics *qdata.Metrics) []lokiEntry {
	out := make([]lokiEntry, 0, len(metrics.GetSeries()))

	for _, series := range metrics.GetSeries() {
		entry := lokiEntry{Stream: nil, Metric: seriesLabels(series), Value: nil, Values: nil}
		for _, point := range series.GetPoints() {
			seconds := float64(point.GetEnd().AsTime().UnixNano()) / nanosPerSecond
			value := strconv.FormatFloat(point.GetValue().GetDoubleValue(), 'f', fullPrecision, floatBitSize)
			entry.Values = append(entry.Values, []any{seconds, value})
		}

		out = append(out, entry)
	}

	return out
}

func seriesLabels(series *qdata.MetricSeries) map[string]string {
	labels := map[string]string{}

	if name := series.GetName(); name != "" {
		labels["__name__"] = name
	}

	for _, attr := range series.GetAttributes().GetValues() {
		labels[attr.GetKey()] = qdata.ValueString(attr.GetValue())
	}

	return labels
}

func writeJSON(writer http.ResponseWriter, status int, payload lokiResponse) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)

	// Headers are already sent, so an encode error can only be logged/dropped.
	err := json.NewEncoder(writer).Encode(payload)
	if err != nil {
		return
	}
}

// writeError mirrors Loki, which returns a plain-text body with the status code.
func writeError(writer http.ResponseWriter, status int, err error) {
	http.Error(writer, err.Error(), status)
}

func writeHandlerError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError

	var coded *qerror.Error
	if errors.As(err, &coded) {
		status = coded.HTTPStatus()
	}

	writeError(writer, status, err)
}
