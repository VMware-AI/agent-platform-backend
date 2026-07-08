package graph

import (
	"context"
	"errors"
	"testing"
)

// TestPoolBreakerRegistry_PerEndpointIsolation: an endpoint that always
// fails must NOT trip the breaker for a different endpoint.
func TestPoolBreakerRegistry_PerEndpointIsolation(t *testing.T) {
	r := newPoolBreakerRegistry(2, 1)

	// Drive endpoint A into Open state.
	cbA := r.get("vc-a")
	for i := 0; i < 5; i++ {
		_, _ = cbA.Execute(func() (any, error) {
			return nil, errors.New("connection refused")
		})
	}
	// endpoint B has a fresh breaker; should still execute normally.
	cbB := r.get("vc-b")
	called := 0
	_, err := cbB.Execute(func() (any, error) {
		called++
		return nil, nil
	})
	if err != nil {
		t.Fatalf("B should be unaffected, got %v", err)
	}
	if called != 1 {
		t.Fatalf("B call = %d, want 1", called)
	}
}

// TestPoolBreakerRegistry_SameEndpointShared: requesting the same
// endpoint twice returns the same breaker object.
func TestPoolBreakerRegistry_SameEndpointShared(t *testing.T) {
	r := newPoolBreakerRegistry(5, 1)
	if r.get("vc") != r.get("vc") {
		t.Fatal("get should be idempotent per endpoint")
	}
}

// TestPoolBreakerRegistry_TripsAfterConsecutiveFailures: a real
// gobreaker integration test — after N consecutive failures the breaker
// enters Open and rejects further calls.
func TestPoolBreakerRegistry_TripsAfterConsecutiveFailures(t *testing.T) {
	// threshold=1: a single failure trips the breaker immediately.
	r := newPoolBreakerRegistry(1, 100)
	cb := r.get("vc")

	// First call fails → trips.
	_, err := cb.Execute(func() (any, error) {
		return nil, errors.New("boom")
	})
	if err == nil {
		t.Fatal("first call should fail")
	}

	// Second call should be rejected with ErrOpenState.
	_, err = cb.Execute(func() (any, error) {
		t.Fatal("this fn must not run while breaker is open")
		return nil, nil
	})
	if err == nil {
		t.Fatal("expected error from open breaker")
	}
	// gobreaker returns its ErrOpenState sentinel; we don't pin the exact
	// type, only that the breaker is now blocking traffic.
}

// TestPoolBreakerRegistry_ThresholdZeroNeverTrips: threshold 0 means the breaker
// is disabled (config doc: "Zero or negative disables the breaker"). Even after
// many consecutive failures it must keep letting requests through — an unguarded
// ReadyToTrip (ConsecutiveFailures >= 0) would be vacuously true and trip on the
// very first failure, the inverse of the doc (#98 item 2).
func TestPoolBreakerRegistry_ThresholdZeroNeverTrips(t *testing.T) {
	r := newPoolBreakerRegistry(0, 100)
	cb := r.get("vc")

	// Drive far more failures than any sane threshold; the breaker must stay closed.
	for i := 0; i < 50; i++ {
		_, _ = cb.Execute(func() (any, error) {
			return nil, errors.New("boom")
		})
	}
	// A subsequent call must still be executed (breaker never opened).
	called := 0
	_, err := cb.Execute(func() (any, error) {
		called++
		return nil, nil
	})
	if err != nil {
		t.Fatalf("disabled breaker rejected a call: %v", err)
	}
	if called != 1 {
		t.Fatalf("disabled breaker did not execute the call (called=%d)", called)
	}
}

// TestPoolBreakerRegistry_TripsOnNthFailure: with threshold N the breaker trips
// on exactly the Nth consecutive failure — not before (calls N-1 and earlier
// still execute) and not after (call N+1 is rejected without running).
func TestPoolBreakerRegistry_TripsOnNthFailure(t *testing.T) {
	const n = 3
	r := newPoolBreakerRegistry(n, 100)
	cb := r.get("vc")

	// The first N-1 failures must NOT trip: each call still runs.
	for i := 1; i < n; i++ {
		ran := false
		_, err := cb.Execute(func() (any, error) {
			ran = true
			return nil, errors.New("boom")
		})
		if !ran {
			t.Fatalf("failure %d/%d should still execute (breaker not yet tripped)", i, n)
		}
		if err == nil {
			t.Fatalf("failure %d should surface the error", i)
		}
	}

	// The Nth failure runs and trips the breaker.
	ranNth := false
	_, err := cb.Execute(func() (any, error) {
		ranNth = true
		return nil, errors.New("boom")
	})
	if !ranNth {
		t.Fatal("the Nth failure should still execute (it is what trips the breaker)")
	}
	if err == nil {
		t.Fatal("the Nth failure should surface the error")
	}

	// Now the breaker is open: the next call is rejected without running.
	_, err = cb.Execute(func() (any, error) {
		t.Fatal("breaker is open; this fn must not run")
		return nil, nil
	})
	if err == nil {
		t.Fatal("call after the Nth failure should be rejected by the open breaker")
	}
}

// TestSyncOnePool_GracefulWhenNoBreaker: when the Resolver hasn't been
// wired with EnablePoolSync, syncOnePool still runs the connect→write
// pipeline (without retry/breaker layers). This is the path tests take.
func TestSyncOnePool_GracefulWhenNoBreaker(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	// Deliberately do NOT call EnablePoolSync — verify the no-plumbing
	// path. r.poolBreakers is nil, r.poolSyncTimeout is 0.
	pool := r.Ent.ResourcePool.Create().
		SetName("noplumb").
		SetEndpoint("https://127.0.0.1:1").
		SaveX(context.Background())
	_, _, err := r.syncOnePool(context.Background(), pool)
	// We expect an error (no listener on 127.0.0.1:1) but NOT a panic.
	// The point of the test is that the no-plumbing path doesn't crash
	// and runs the connect step.
	if err == nil {
		t.Fatal("expected dial-refused error")
	}
}
