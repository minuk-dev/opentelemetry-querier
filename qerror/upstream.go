package qerror

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
)

// UpstreamTransportError distinguishes canceled and timed-out requests from unavailable backends.
func UpstreamTransportError(err error) *Error {
	code := CodeUnavailable

	var networkError net.Error

	switch {
	case errors.Is(err, context.Canceled):
		code = CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		code = CodeDeadlineExceeded
	case errors.As(err, &networkError) && networkError.Timeout():
		code = CodeDeadlineExceeded
	}

	return New(code, "upstream request: %v", err)
}

// UpstreamHTTPError classifies backend errors without attributing configured credentials to the caller.
func UpstreamHTTPError(status int, body []byte) *Error {
	code := upstreamStatusCode(status)
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		// Backend credentials and quotas retain their status-derived classification.
	default:
		var envelope struct {
			ErrorType string `json:"errorType"`
		}

		if json.Unmarshal(body, &envelope) == nil {
			switch envelope.ErrorType {
			case "bad_data":
				code = CodeInvalidArgument
			case "execution":
				code = CodeQueryExecution
			case "timeout":
				code = CodeDeadlineExceeded
			case "canceled":
				code = CodeCanceled
			}
		}
	}

	return New(code, "upstream status %d: %s", status, string(body))
}

func upstreamStatusCode(status int) Code {
	switch status {
	case http.StatusBadRequest:
		return CodeInvalidArgument
	case http.StatusUnprocessableEntity:
		return CodeQueryExecution
	case http.StatusTooManyRequests:
		return CodeResourceExhausted
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return CodeDeadlineExceeded
	default:
		return CodeUnavailable
	}
}
