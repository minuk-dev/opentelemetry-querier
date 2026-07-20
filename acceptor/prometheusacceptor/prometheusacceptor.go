// Package prometheusacceptor implements an acceptor that speaks the Prometheus HTTP
// query API. It is the ingress counterpart of the prometheusdispatcher: clients that
// already speak Prometheus (`/api/v1/query`, `/api/v1/query_range`) can query
// through the proxy. Requests are parsed into a qdata Query (PromQL dialect),
// run through the pipeline, and the qdata Result is serialized back into the
// Prometheus JSON response envelope.
package prometheusacceptor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/minuk-dev/opentelemetry-querier/component"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// DefaultEndpoint is the default HTTP listen address (the canonical Prometheus
// port). Override it when a real Prometheus already listens on 9090.
const DefaultEndpoint = "0.0.0.0:9090"

const (
	readHeaderTimeout = 10 * time.Second
	nanosPerSecond    = 1e9
	floatBitSize      = 64
	fullPrecision     = -1

	errorTypeInternal = "internal"
)

var (
	errMissingQuery = errors.New("prometheusacceptor: missing 'query' parameter")
	errBadTime      = errors.New("prometheusacceptor: invalid time parameter")
	errBadStep      = errors.New("prometheusacceptor: invalid 'step' parameter")
)

// Config configures the Prometheus acceptor.
type Config struct {
	// Endpoint is the HTTP listen address.
	Endpoint string `mapstructure:"endpoint"`
}

// Acceptor serves the Prometheus HTTP query API.
type Acceptor struct {
	cfg     Config
	handler pipeline.Handler
	server  *http.Server
}

// New builds a Prometheus acceptor bound to the given pipeline Handler.
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
	mux.HandleFunc("/api/v1/query", a.handleInstant)
	mux.HandleFunc("/api/v1/query_range", a.handleRange)
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})

	return mux
}

// Start binds the listener and serves in the background.
func (a *Acceptor) Start(ctx context.Context, _ component.Host) error {
	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(ctx, "tcp", a.cfg.Endpoint)
	if err != nil {
		return fmt.Errorf("prometheusacceptor: listen %s: %w", a.cfg.Endpoint, err)
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
		return fmt.Errorf("prometheusacceptor: shutdown: %w", err)
	}

	return nil
}

func (a *Acceptor) handleInstant(writer http.ResponseWriter, request *http.Request) {
	query, err := parseInstant(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "bad_data", err)

		return
	}

	a.serve(writer, request, query, "vector")
}

func (a *Acceptor) handleRange(writer http.ResponseWriter, request *http.Request) {
	query, err := parseRange(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "bad_data", err)

		return
	}

	a.serve(writer, request, query, "matrix")
}

func (a *Acceptor) serve(writer http.ResponseWriter, request *http.Request, query *qdata.Query, resultType string) {
	result, err := a.handler.Handle(request.Context(), query)
	if err != nil {
		writeHandlerError(writer, err)

		return
	}

	writeJSON(writer, http.StatusOK, promResponse{
		Status:    "success",
		Data:      &promData{ResultType: resultType, Result: metricsToResult(result.GetMetrics(), resultType)},
		ErrorType: "",
		Error:     "",
		Warnings:  feedbackWarnings(result.GetFeedback()),
	})
}

// ---- request parsing ----

func parseInstant(request *http.Request) (*qdata.Query, error) {
	err := request.ParseForm()
	if err != nil {
		return nil, fmt.Errorf("prometheusacceptor: parse form: %w", err)
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

	query := &qdata.Query{Signal: qdata.SignalMetrics, Context: qdata.ContextInstant, Expr: expr, Dialect: "promql"}
	query.Range = &qdata.TimeRange{Start: nil, End: timestamppb.New(evalAt), StartExclusive: false, EndExclusive: false}
	injectHeaders(query, request.Header)

	return query, nil
}

func parseRange(request *http.Request) (*qdata.Query, error) {
	err := request.ParseForm()
	if err != nil {
		return nil, fmt.Errorf("prometheusacceptor: parse form: %w", err)
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

	step, err := parseStep(request.Form.Get("step"))
	if err != nil {
		return nil, err
	}

	query := &qdata.Query{Signal: qdata.SignalMetrics, Context: qdata.ContextRange, Expr: expr, Dialect: "promql"}
	query.Range = &qdata.TimeRange{
		Start:          timestamppb.New(start),
		End:            timestamppb.New(end),
		StartExclusive: false,
		EndExclusive:   false,
	}
	query.Step = durationpb.New(step)
	injectHeaders(query, request.Header)

	return query, nil
}

func parseTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, fmt.Errorf("%w: empty", errBadTime)
	}

	seconds, err := strconv.ParseFloat(raw, floatBitSize)
	if err == nil {
		return time.Unix(0, int64(seconds*nanosPerSecond)), nil
	}

	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %q", errBadTime, raw)
	}

	return parsed, nil
}

func parseStep(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, errBadStep
	}

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

type promResponse struct {
	Status    string    `json:"status"`
	Data      *promData `json:"data,omitempty"`
	ErrorType string    `json:"errorType,omitempty"`
	Error     string    `json:"error,omitempty"`
	Warnings  []string  `json:"warnings,omitempty"`
}

type promData struct {
	ResultType string       `json:"resultType"`
	Result     []promSeries `json:"result"`
}

type promSeries struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value,omitempty"`
	Values [][]any           `json:"values,omitempty"`
}

func metricsToResult(metrics *qdata.Metrics, resultType string) []promSeries {
	seriesList := metrics.GetSeries()
	out := make([]promSeries, 0, len(seriesList))

	for _, series := range seriesList {
		entry := promSeries{Metric: seriesLabels(series), Value: nil, Values: nil}
		points := series.GetPoints()

		if resultType == "matrix" {
			for _, point := range points {
				entry.Values = append(entry.Values, sample(point))
			}
		} else if len(points) > 0 {
			entry.Value = sample(points[len(points)-1])
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

func sample(point *qdata.MetricPoint) []any {
	seconds := float64(point.GetEnd().AsTime().UnixNano()) / nanosPerSecond
	value := strconv.FormatFloat(point.GetValue().GetDoubleValue(), 'f', fullPrecision, floatBitSize)

	return []any{seconds, value}
}

func feedbackWarnings(feedback *qdata.Feedback) []string {
	notifications := feedback.GetNotifications()
	if len(notifications) == 0 {
		return nil
	}

	warnings := make([]string, 0, len(notifications))
	for _, note := range notifications {
		warnings = append(warnings, note.GetMessage())
	}

	return warnings
}

func writeJSON(writer http.ResponseWriter, status int, payload promResponse) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)

	// Headers are already sent, so an encode error can only be logged/dropped.
	err := json.NewEncoder(writer).Encode(payload)
	if err != nil {
		return
	}
}

func writeError(writer http.ResponseWriter, status int, errorType string, err error) {
	writeJSON(writer, status, promResponse{
		Status:    "error",
		Data:      nil,
		ErrorType: errorType,
		Error:     err.Error(),
		Warnings:  nil,
	})
}

func writeHandlerError(writer http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	errorType := errorTypeInternal

	var coded *qerror.Error
	if errors.As(err, &coded) {
		status = coded.HTTPStatus()
		errorType = promErrorType(coded.Code)
	}

	writeError(writer, status, errorType, err)
}

func promErrorType(code qerror.Code) string {
	switch code {
	case qerror.CodeInvalidArgument:
		return "bad_data"
	case qerror.CodeUnauthenticated, qerror.CodePermissionDenied:
		return "unauthorized"
	case qerror.CodeResourceExhausted:
		return "too_many_requests"
	case qerror.CodeUnavailable, qerror.CodeDeadlineExceeded:
		return "unavailable"
	case qerror.CodeInternal:
		return errorTypeInternal
	default:
		return errorTypeInternal
	}
}
