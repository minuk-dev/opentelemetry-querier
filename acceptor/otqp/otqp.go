// Package otqp implements the default acceptor: the OpenTelemetry Query Protocol
// (OTQP), served over both gRPC and HTTP. It is the query-side analogue of an
// OTLP receiver. A single QueryRequest carries a qdata Query; the acceptor runs
// it through the pipeline Handler and returns a QueryResponse carrying the qdata
// Result (with its feedback side channel).
package otqp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/minuk-dev/opentelemetry-querier/component"
	otqpv1 "github.com/minuk-dev/opentelemetry-querier/gen/otqp/v1"
	"github.com/minuk-dev/opentelemetry-querier/pipeline"
	"github.com/minuk-dev/opentelemetry-querier/qdata"
	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

// Default OTQP endpoints, mirroring OTLP's 4317 (gRPC) / 4318 (HTTP) with a "27"
// query twist.
const (
	DefaultGRPCEndpoint = "0.0.0.0:4327"
	DefaultHTTPEndpoint = "0.0.0.0:4328"
	// HTTPQueryPath is the OTQP/HTTP unary query endpoint.
	HTTPQueryPath = "/v1/query"
)

// Config configures the OTQP acceptor. An empty endpoint disables that transport.
type Config struct {
	GRPCEndpoint string `yaml:"grpc_endpoint"`
	HTTPEndpoint string `yaml:"http_endpoint"`
}

// Acceptor serves OTQP over gRPC and/or HTTP.
type Acceptor struct {
	cfg     Config
	handler pipeline.Handler

	grpcServer *grpc.Server
	httpServer *http.Server
}

// New builds an OTQP acceptor bound to the given pipeline Handler.
func New(cfg Config, handler pipeline.Handler) *Acceptor {
	if cfg.GRPCEndpoint == "" && cfg.HTTPEndpoint == "" {
		cfg.GRPCEndpoint = DefaultGRPCEndpoint
		cfg.HTTPEndpoint = DefaultHTTPEndpoint
	}
	return &Acceptor{cfg: cfg, handler: handler}
}

func (a *Acceptor) Name() string { return "otqp" }

// Start binds the configured listeners and serves in the background.
func (a *Acceptor) Start(_ context.Context, _ component.Host) error {
	if a.cfg.GRPCEndpoint != "" {
		lis, err := net.Listen("tcp", a.cfg.GRPCEndpoint)
		if err != nil {
			return fmt.Errorf("otqp: listen grpc %s: %w", a.cfg.GRPCEndpoint, err)
		}
		a.grpcServer = grpc.NewServer()
		otqpv1.RegisterQueryServiceServer(a.grpcServer, &grpcService{handler: a.handler})
		go func() { _ = a.grpcServer.Serve(lis) }()
	}

	if a.cfg.HTTPEndpoint != "" {
		mux := http.NewServeMux()
		mux.HandleFunc(HTTPQueryPath, a.handleHTTPQuery)
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		a.httpServer = &http.Server{Addr: a.cfg.HTTPEndpoint, Handler: mux}
		lis, err := net.Listen("tcp", a.cfg.HTTPEndpoint)
		if err != nil {
			return fmt.Errorf("otqp: listen http %s: %w", a.cfg.HTTPEndpoint, err)
		}
		go func() { _ = a.httpServer.Serve(lis) }()
	}
	return nil
}

// Shutdown gracefully stops both transports.
func (a *Acceptor) Shutdown(ctx context.Context) error {
	if a.httpServer != nil {
		_ = a.httpServer.Shutdown(ctx)
	}
	if a.grpcServer != nil {
		a.grpcServer.GracefulStop()
	}
	return nil
}

// ---- gRPC transport ----

type grpcService struct {
	otqpv1.UnimplementedQueryServiceServer
	handler pipeline.Handler
}

func (s *grpcService) Query(ctx context.Context, req *otqpv1.QueryRequest) (*otqpv1.QueryResponse, error) {
	result, err := s.handler.Handle(ctx, req.GetQuery())
	if err != nil {
		return nil, status.Error(grpcCode(err), err.Error())
	}
	return &otqpv1.QueryResponse{Result: result}, nil
}

// QueryStream evaluates a streaming-context query. The single-window MVP emits
// one response; a future streaming dispatcher would emit one per flushed window.
func (s *grpcService) QueryStream(req *otqpv1.QueryRequest, stream grpc.ServerStreamingServer[otqpv1.QueryResponse]) error {
	result, err := s.handler.Handle(stream.Context(), req.GetQuery())
	if err != nil {
		return status.Error(grpcCode(err), err.Error())
	}
	return stream.Send(&otqpv1.QueryResponse{Result: result})
}

func grpcCode(err error) codes.Code {
	switch qerror.CodeOf(err) {
	case qerror.CodeInvalidArgument:
		return codes.InvalidArgument
	case qerror.CodeUnauthenticated:
		return codes.Unauthenticated
	case qerror.CodePermissionDenied:
		return codes.PermissionDenied
	case qerror.CodeResourceExhausted:
		return codes.ResourceExhausted
	case qerror.CodeUnavailable:
		return codes.Unavailable
	case qerror.CodeDeadlineExceeded:
		return codes.DeadlineExceeded
	default:
		return codes.Internal
	}
}

// ---- HTTP transport ----

// handleHTTPQuery serves OTQP/HTTP: POST HTTPQueryPath with a QueryRequest body
// encoded as protobuf (application/x-protobuf) or JSON (application/json).
func (a *Acceptor) handleHTTPQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	useJSON := isJSON(r.Header.Get("Content-Type"))
	req := &otqpv1.QueryRequest{}
	if useJSON {
		err = protojson.Unmarshal(body, req)
	} else {
		err = proto.Unmarshal(body, req)
	}
	if err != nil {
		http.Error(w, "decode request: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Carry inbound HTTP headers onto the query so downstream processors (auth,
	// tenant) can read them.
	injectHeaders(req.GetQuery(), r.Header)

	result, herr := a.handler.Handle(r.Context(), req.GetQuery())
	if herr != nil {
		writeHTTPError(w, herr)
		return
	}

	resp := &otqpv1.QueryResponse{Result: result}
	var out []byte
	if useJSON {
		w.Header().Set("Content-Type", "application/json")
		out, err = protojson.Marshal(resp)
	} else {
		w.Header().Set("Content-Type", "application/x-protobuf")
		out, err = proto.Marshal(resp)
	}
	if err != nil {
		http.Error(w, "encode response: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(out)
}

func writeHTTPError(w http.ResponseWriter, err error) {
	var qe *qerror.Error
	if errors.As(err, &qe) {
		http.Error(w, qe.Msg, qe.HTTPStatus())
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func isJSON(contentType string) bool {
	// Treat anything that isn't explicitly protobuf as JSON, matching OTLP/HTTP's
	// lenient content negotiation.
	for i := 0; i < len(contentType); i++ {
		if contentType[i] == ';' {
			contentType = contentType[:i]
			break
		}
	}
	return contentType != "application/x-protobuf" && contentType != "application/protobuf"
}

func injectHeaders(q *qdata.Query, h http.Header) {
	if q == nil || len(h) == 0 {
		return
	}
	if q.Header == nil {
		q.Header = make(map[string]*qdata.HeaderValues, len(h))
	}
	for k, v := range h {
		q.Header[k] = &qdata.HeaderValues{Values: v}
	}
}
