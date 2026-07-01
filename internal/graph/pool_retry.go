package graph

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"github.com/VMware-AI/agent-platform-backend/internal/vcenter"
)

// retrySync invokes fn up to maxRetries+1 times. Between attempts it sleeps
// with exponential backoff (1s, 2s, 4s, …) plus up-to-25% jitter. Retries
// only fire for errors wrapped as *vcenter.RetryableError; business errors
// (auth failure, object-not-found, etc.) return immediately and consume no
// retry budget. ctx cancellation breaks out of the loop early.
//
// The first attempt is also the only attempt when maxRetries == 0; we never
// retry a successful call.
func retrySync(ctx context.Context, maxRetries int, fn func(context.Context) error) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := fn(ctx)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryable(err) {
			return err
		}
		if attempt == maxRetries {
			break
		}
		backoff := time.Duration(1<<attempt) * time.Second // 1s, 2s, 4s
		// Jitter ceiling is backoff/4; we MUST defend against rand.Int63n(0)
		// (the Go runtime panics on a zero argument). When the ceiling would
		// be zero, sleep the plain backoff without jitter.
		var jitter time.Duration
		if max := int64(backoff / 4); max > 0 {
			jitter = time.Duration(rand.Int63n(max))
		}
		select {
		case <-time.After(backoff + jitter):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return lastErr
}

// isRetryable decides whether a sync error should be retried. The transport
// layer (vcenter.RetryableError) owns the classification; context errors are
// left alone so the caller's timeout/deadline is honoured verbatim.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var re *vcenter.RetryableError
	return errors.As(err, &re)
}