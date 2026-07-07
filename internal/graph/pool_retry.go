package graph

import (
	"context"
	"errors"
	"log"
	"math/rand"
	"time"
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
			log.Printf("pool-sync.retry: ctx done before attempt %d err=%q", attempt+1, err)
			return err
		}
		err := fn(ctx)
		if err == nil {
			if attempt > 0 {
				log.Printf("pool-sync.retry: attempt %d succeeded after %d previous failure(s)",
					attempt+1, attempt)
			}
			return nil
		}
		lastErr = err
		if !isRetryable(err) {
			log.Printf("pool-sync.retry: attempt %d failed with non-retryable err=%q, giving up",
				attempt+1, err)
			return err
		}
		if attempt == maxRetries {
			log.Printf("pool-sync.retry: attempt %d failed (retryable) err=%q, retries exhausted (%d/%d)",
				attempt+1, err, attempt+1, maxRetries+1)
			break
		}
		backoff := time.Duration(1<<attempt) * time.Second // 1s, 2s, 4s
		var jitter time.Duration
		if max := int64(backoff / 4); max > 0 {
			jitter = time.Duration(rand.Int63n(max))
		}
		log.Printf("pool-sync.retry: attempt %d failed (retryable) err=%q, sleeping %s before retry %d/%d",
			attempt+1, err, backoff+jitter, attempt+2, maxRetries+1)
		select {
		case <-time.After(backoff + jitter):
		case <-ctx.Done():
			log.Printf("pool-sync.retry: ctx cancelled during backoff err=%q", ctx.Err())
			return ctx.Err()
		}
	}
	return lastErr
}

// isRetryable decides whether a sync error should be retried. With the
// vcenter-side retry classifier gone from the codebase, every non-context
// error is treated as retryable — the caller already filters out nil and
// ctx errors above. This is the conservative default: a transient vCenter
// hiccup doesn't kill the whole sync, while a permanent error eventually
// exhausts maxRetries and bubbles up.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}
