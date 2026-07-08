package graph

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestRetrySync_ExhaustsRetriesOnRetryable: a function that always returns
// a retryable error must consume the full retry budget and bubble up the
// last error.
func TestRetrySync_ExhaustsRetriesOnRetryable(t *testing.T) {
	calls := 0
	err := retrySync(context.Background(), 2, func(ctx context.Context) error {
		calls++
		return errors.New("connection refused")
	})
	if err == nil {
		t.Fatal("expected non-nil after exhausting retries")
	}
	if calls != 3 { // 1 initial + 2 retries
		t.Fatalf("calls = %d, want 3", calls)
	}
}

// TestRetrySync_ImmediateReturnOnContextError: with the vcenter-side error
// classifier gone, the ONLY non-retryable class left is a context error
// (isRetryable: errors.Is Canceled/DeadlineExceeded) — a wrapped one must
// short-circuit the loop without burning backoff sleeps. (The old
// "business errors return immediately" contract no longer exists in source;
// whether permission-denied/not-found should regain a fast path is an open
// question for the retry author.)
func TestRetrySync_ImmediateReturnOnContextError(t *testing.T) {
	calls := 0
	err := retrySync(context.Background(), 5, func(ctx context.Context) error {
		calls++
		return fmt.Errorf("sync aborted: %w", context.DeadlineExceeded)
	})
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if calls != 1 {
		t.Fatalf("context error should be returned after 1 call, got %d", calls)
	}
}

// TestRetrySync_StopsOnContextCancel: a context cancellation between
// attempts breaks the retry loop promptly.
func TestRetrySync_StopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	calls := 0
	err := retrySync(ctx, 5, func(ctx context.Context) error {
		calls++
		return errors.New("transient")
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	// At most 2 calls: the first plus one attempt that observes the cancel
	// on the next loop iteration (the inner ctx.Err() check before sleeping).
	if calls > 2 {
		t.Fatalf("calls = %d, want ≤ 2 (cancelled retry)", calls)
	}
}

// TestRetrySync_SuccessAfterRetries: a transient error followed by a
// successful attempt must return nil.
func TestRetrySync_SuccessAfterRetries(t *testing.T) {
	calls := 0
	err := retrySync(context.Background(), 3, func(ctx context.Context) error {
		calls++
		if calls < 2 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil after success, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

// TestRetrySync_NilErrorFastPath: a nil error returns immediately without
// waiting for retries.
func TestRetrySync_NilErrorFastPath(t *testing.T) {
	calls := 0
	err := retrySync(context.Background(), 5, func(ctx context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("nil error should return after 1 call, got %d", calls)
	}
}
