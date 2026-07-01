package vcenter

import (
	"errors"
	"net"
	"testing"
)

// MaybeRetryable classifies a vCenter error as transient (retryable) or
// not. The list is intentionally narrow: only well-known connection-
// refused / timeout / network-down patterns. Auth failures, object-not-
// found and SOAP fault strings that look like "Permission denied" or
// "InvalidLogin" should never be classified as retryable.
func TestMaybeRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"connection refused", errors.New("dial tcp 1.2.3.4:443: connect: connection refused"), true},
		{"i/o timeout", errors.New("read tcp 1.2.3.4:443: i/o timeout"), true},
		{"connection reset", errors.New("read tcp 1.2.3.4:443: read: connection reset by peer"), true},
		{"EOF", errors.New("unexpected EOF"), true},
		{"no route", errors.New("dial tcp: no route to host"), true},
		{"network unreachable", errors.New("dial tcp: network is unreachable"), true},
		{"temp unavailable", errors.New("server is temporarily unavailable"), true},
		{"auth fail", errors.New("ServerFaultCode: Cannot complete login due to an incorrect user name or password."), false},
		{"not found", errors.New("The object 'vim.Folder:group-d1' wasn't found"), false},
		{"unrelated", errors.New("some other transient-looking message"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MaybeRetryable(c.err)
			if c.want {
				if _, ok := got.(*RetryableError); !ok {
					t.Fatalf("MaybeRetryable(%v) = %v, want *RetryableError", c.err, got)
				}
			} else if got != nil {
				// either passthrough (non-nil original) or nil
				if _, ok := got.(*RetryableError); ok {
					t.Fatalf("MaybeRetryable(%v) = %T, want passthrough", c.err, got)
				}
			}
		})
	}
}

// TestRetryableErrorUnwrap ensures the standard library's errors.Is and
// errors.Unwrap walk through the wrapper correctly so retrySync's
// isRetryable can detect a wrapped retryable error.
func TestRetryableErrorUnwrap(t *testing.T) {
	base := errors.New("dial tcp: connection refused")
	wrapped := MaybeRetryable(base)
	if wrapped == nil {
		t.Fatal("MaybeRetryable should classify 'connection refused' as retryable")
	}
	if !errors.Is(wrapped, base) {
		t.Fatal("errors.Is should find the wrapped base error")
	}
	if !errors.Is(wrapped, wrapped) {
		t.Fatal("errors.Is should find itself")
	}
	// Sanity: net.OpError also unwraps cleanly via standard library.
	opErr := &net.OpError{Op: "dial", Err: errors.New("connection refused")}
	wrappedOp := MaybeRetryable(opErr)
	if _, ok := wrappedOp.(*RetryableError); !ok {
		t.Fatalf("net.OpError with refused should be retryable, got %T", wrappedOp)
	}
}
