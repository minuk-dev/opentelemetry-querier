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
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
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

// readHeaderTimeout bounds how long the HTTP server waits for request headers
// (mitigates Slowloris).
const readHeaderTimeout = 10 * time.Second

// Config configures the OTQP acceptor. An empty endpoint disables that transport.
type Config struct {
	GRPCEndpoint string `mapstructure:"grpc_endpoint"`
	HTTPEndpoint string `mapstructure:"http_endpoint"`
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

	return &Acceptor{cfg: cfg, handler: handler, grpcServer: nil, httpServer: nil}
}

// Start binds the configured listeners and serves in the background.
func (a *Acceptor) Start(ctx context.Context, _ component.Host) error {
	var listenConfig net.ListenConfig

	if a.cfg.GRPCEndpoint != "" {
		listener, err := listenConfig.Listen(ctx, "tcp", a.cfg.GRPCEndpoint)
		if err != nil {
			return fmt.Errorf("otqp: listen grpc %s: %w", a.cfg.GRPCEndpoint, err)
		}

		a.grpcServer = grpc.NewServer()
		otqpv1.RegisterQueryServiceServer(a.grpcServer, &grpcService{
			UnimplementedQueryServiceServer: otqpv1.UnimplementedQueryServiceServer{},
			handler:                         a.handler,
		})

		go func() { _ = a.grpcServer.Serve(listener) }()
	}

	if a.cfg.HTTPEndpoint != "" {
		mux := http.NewServeMux()
		mux.HandleFunc(HTTPQueryPath, a.handleHTTPQuery)
		mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusOK)
		})

		a.httpServer = &http.Server{
			Addr:              a.cfg.HTTPEndpoint,
			Handler:           mux,
			ReadHeaderTimeout: readHeaderTimeout,
		}

		listener, err := listenConfig.Listen(ctx, "tcp", a.cfg.HTTPEndpoint)
		if err != nil {
			return fmt.Errorf("otqp: listen http %s: %w", a.cfg.HTTPEndpoint, err)
		}

		go func() { _ = a.httpServer.Serve(listener) }()
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
	injectMetadata(ctx, req.GetQuery())

	result, err := s.handler.Handle(ctx, req.GetQuery())
	if err != nil {
		return nil, status.Error(grpcCode(err), err.Error())
	}

	return &otqpv1.QueryResponse{Result: result}, nil
}

// QueryStream evaluates a streaming-context query. The single-window MVP emits
// one response; a future streaming dispatcher would emit one per flushed window.
func (s *grpcService) QueryStream(
	req *otqpv1.QueryRequest,
	stream grpc.ServerStreamingServer[otqpv1.QueryResponse],
) error {
	injectMetadata(stream.Context(), req.GetQuery())

	result, err := s.handler.Handle(stream.Context(), req.GetQuery())
	if err != nil {
		return status.Error(grpcCode(err), err.Error())
	}

	sendErr := stream.Send(&otqpv1.QueryResponse{Result: result})
	if sendErr != nil {
		return fmt.Errorf("otqp: send response: %w", sendErr)
	}

	return nil
}

func grpcCode(err error) codes.Code {
	switch qerror.CodeOf(err) {
	case qerror.CodeInternal:
		return codes.Internal
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
func (a *Acceptor) handleHTTPQuery(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)

		return
	}

	body, err := io.ReadAll(request.Body)
	if err != nil {
		http.Error(writer, "read body: "+err.Error(), http.StatusBadRequest)

		return
	}

	useJSON := isJSON(request.Header.Get("Content-Type"))

	req := &otqpv1.QueryRequest{}
	if useJSON {
		err = protojson.Unmarshal(body, req)
	} else {
		err = proto.Unmarshal(body, req)
	}

	if err != nil {
		http.Error(writer, "decode request: "+err.Error(), http.StatusBadRequest)

		return
	}

	// Carry inbound HTTP headers onto the query so downstream processors (auth,
	// tenant) can read them. injectMetadata is the gRPC analogue.
	injectHeaders(req.GetQuery(), request.Header)

	result, err := a.handler.Handle(request.Context(), req.GetQuery())
	if err != nil {
		writeHTTPError(writer, err)

		return
	}

	writeResponse(writer, useJSON, &otqpv1.QueryResponse{Result: result})
}

func writeResponse(writer http.ResponseWriter, useJSON bool, resp *otqpv1.QueryResponse) {
	var (
		out         []byte
		err         error
		contentType string
	)

	if useJSON {
		contentType = "application/json"
		out, err = protojson.Marshal(resp)
	} else {
		contentType = "application/x-protobuf"
		out, err = proto.Marshal(resp)
	}

	if err != nil {
		http.Error(writer, "encode response: "+err.Error(), http.StatusInternalServerError)

		return
	}

	writer.Header().Set("Content-Type", contentType)
	_, _ = writer.Write(out)
}

func writeHTTPError(writer http.ResponseWriter, err error) {
	var codedErr *qerror.Error
	if errors.As(err, &codedErr) {
		http.Error(writer, codedErr.Msg, codedErr.HTTPStatus())

		return
	}

	http.Error(writer, err.Error(), http.StatusInternalServerError)
}

func isJSON(contentType string) bool {
	// Treat anything that isn't explicitly protobuf as JSON, matching OTLP/HTTP's
	// lenient content negotiation.
	mediaType, _, _ := strings.Cut(contentType, ";")

	return mediaType != "application/x-protobuf" && mediaType != "application/protobuf"
}

// injectMetadata carries inbound gRPC metadata onto the query as headers, the
// gRPC analogue of injectHeaders on the HTTP path. Without this, header-based
// processors (auth, tenant) would see nothing over gRPC, since a gRPC client
// sends credentials as metadata rather than in the request body.
// metadata.MD and http.Header share the map[string][]string underlying type, so
// the same injector applies; downstream lookups are case-insensitive, so gRPC's
// lower-cased keys still match canonical header names.
func injectMetadata(ctx context.Context, query *qdata.Query) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return
	}

	injectHeaders(query, http.Header(md))
}

func injectHeaders(query *qdata.Query, header http.Header) {
	if query == nil || len(header) == 0 {
		return
	}

	if query.Header == nil {
		query.Header = make(map[string]*qdata.HeaderValues, len(header))
	}

	for key, values := range header {
		query.Header[key] = &qdata.HeaderValues{Values: values}
	}
}
