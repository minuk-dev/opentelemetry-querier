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
	switch e.Code {
	case CodeInternal:
		return http.StatusInternalServerError
	case CodeInvalidArgument:
		return http.StatusBadRequest
	case CodeUnauthenticated:
		return http.StatusUnauthorized
	case CodePermissionDenied:
		return http.StatusForbidden
	case CodeResourceExhausted:
		return http.StatusTooManyRequests
	case CodeUnavailable:
		return http.StatusBadGateway
	case CodeDeadlineExceeded:
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}

// CodeOf extracts the Code from an error, defaulting to CodeInternal.
func CodeOf(err error) Code {
	var codedErr *Error
	if errors.As(err, &codedErr) {
		return codedErr.Code
	}

	return CodeInternal
}
