package qerror_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/minuk-dev/opentelemetry-querier/qerror"
)

func TestUpstreamTransportError(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		err  error
		code qerror.Code
	}{
		{name: "canceled", err: fmt.Errorf("wrapped: %w", context.Canceled), code: qerror.CodeCanceled},
		{name: "deadline", err: context.DeadlineExceeded, code: qerror.CodeDeadlineExceeded},
		{name: "client timeout", err: &net.DNSError{
			UnwrapErr: nil, Err: "", Name: "", Server: "", IsTimeout: true, IsTemporary: false, IsNotFound: false,
		}, code: qerror.CodeDeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := qerror.UpstreamTransportError(test.err)
			if got.Code != test.code {
				t.Fatalf("code = %v, want %v", got.Code, test.code)
			}
		})
	}
}

func TestUpstreamErrorType(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, body string
		status     int
		code       qerror.Code
	}{
		{name: "timeout", body: `{"errorType":"timeout"}`, status: http.StatusServiceUnavailable,
			code: qerror.CodeDeadlineExceeded},
		{name: "canceled", body: `{"errorType":"canceled"}`, status: http.StatusServiceUnavailable,
			code: qerror.CodeCanceled},
		{name: "execution", body: `{"errorType":"execution"}`, status: http.StatusUnprocessableEntity,
			code: qerror.CodeQueryExecution},
		{name: "backend credentials", body: `{"errorType":"bad_data"}`, status: http.StatusUnauthorized,
			code: qerror.CodeUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := qerror.UpstreamHTTPError(test.status, []byte(test.body))
			if got.Code != test.code {
				t.Fatalf("code = %v, want %v", got.Code, test.code)
			}
		})
	}
}
