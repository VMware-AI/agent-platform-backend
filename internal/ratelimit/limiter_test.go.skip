package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestMemory_BlocksAfterThreshold(t *testing.T) {
	ctx := context.Background()
	l := NewMemory(3, time.Hour)
	k := "login:alice|1.2.3.4"

	for i := 0; i < 3; i++ {
		if l.Blocked(ctx, k) {
			t.Fatalf("blocked too early at attempt %d", i)
		}
		l.Fail(ctx, k)
	}
	if !l.Blocked(ctx, k) {
		t.Fatal("should be blocked after 3 failures")
	}
	if l.Blocked(ctx, "login:bob|1.2.3.4") {
		t.Fatal("a different key must not be blocked (no cross-account/IP DoS)")
	}
	l.Reset(ctx, k)
	if l.Blocked(ctx, k) {
		t.Fatal("reset (successful login) should clear the block")
	}
}

func TestMemory_WindowExpires(t *testing.T) {
	ctx := context.Background()
	l := NewMemory(2, time.Minute)
	now := time.Unix(1000, 0)
	l.now = func() time.Time { return now }
	k := "x"

	l.Fail(ctx, k)
	l.Fail(ctx, k)
	if !l.Blocked(ctx, k) {
		t.Fatal("blocked within window expected")
	}
	now = now.Add(2 * time.Minute) // window elapsed
	if l.Blocked(ctx, k) {
		t.Fatal("block should expire after the window")
	}
}
