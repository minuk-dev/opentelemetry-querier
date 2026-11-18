// Package qerror defines the transport-agnostic error type that flows out of
// processors and the pipeline. Acceptors map its Code onto their transport
// (HTTP status, gRPC code). Keeping it here avoids import cycles between the
// pipeline, processors and acceptors.
package qerror

import (
	"errors"
	"fmt"
	"net/http"
)

// Code is a transport-agnostic error class, loosely aligned with gRPC codes.
type Code int

// Error classes, loosely aligned with gRPC codes.
const (
	CodeInternal Code = iota
	CodeInvalidArgument
	CodeUnauthenticated
	CodePermissionDenied
	CodeResourceExhausted // rate limited
	CodeUnavailable       // upstream/backend failure
	CodeDeadlineExceeded
	CodeCanceled
	CodeQueryExecution
)

// Error carries a Code and a message. Processors return it to short-circuit the
// pipeline (e.g. auth failure, rate limit).
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// New builds a coded error.
func New(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// HTTPStatus maps a Code onto an HTTP status code.
func (e *Error) HTTPStatus() int {
	const clientClosedRequest = 499

	status, ok := map[Code]int{
		CodeInternal:          http.StatusInternalServerError,
		CodeInvalidArgument:   http.StatusBadRequest,
		CodeUnauthenticated:   http.StatusUnauthorized,
		CodePermissionDenied:  http.StatusForbidden,
		CodeResourceExhausted: http.StatusTooManyRequests,
		CodeUnavailable:       http.StatusBadGateway,
		CodeDeadlineExceeded:  http.StatusGatewayTimeout,
		CodeCanceled:          clientClosedRequest,
		CodeQueryExecution:    http.StatusUnprocessableEntity,
	}[e.Code]
	if !ok {
		return http.StatusInternalServerError
	}

	return status
}

// CodeOf extracts the Code from an error, defaulting to CodeInternal.
func CodeOf(err error) Code {
	var codedErr *Error
	if errors.As(err, &codedErr) {
		return codedErr.Code
	}

	return CodeInternal
}
