package graph

import (
	"context"
	"testing"
	"time"

	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
)

// seedRequestLog inserts one request-log row at a controlled created_at so tests
// can place rows in specific buckets (the RecordRequestLog mutation always uses
// time.Now, which is unusable for windowed-aggregation tests).
func seedRequestLog(t *testing.T, c *ent.Client, at time.Time, status, latency, in, out int) {
	t.Helper()
	if _, err := c.RequestLog.Create().
		SetRequestID(at.Format(time.RFC3339Nano)).
		SetStatusCode(status).
		SetLatencyMs(latency).
		SetInputTokens(in).
		SetOutputTokens(out).
		SetCreatedAt(at).
		Save(context.Background()); err != nil {
		t.Fatalf("seed request log: %v", err)
	}
}

// TestRequestMetrics_EmptyWindow: a window with zero matching rows yields no
// buckets with traffic, a zeroed summary, and errorRate 0 (divide-by-zero guard).
func TestRequestMetrics_EmptyWindow(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	from := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	to := from.Add(3 * time.Hour)
	res, err := qr.RequestMetrics(context.Background(), from, to, model.RequestMetricsBucketGranularityHour, nil)
	if err != nil {
		t.Fatalf("RequestMetrics empty: %v", err)
	}
	// Gap-fill still produces one bucket per hour (3 here), all zeroed.
	if len(res.Buckets) != 3 {
		t.Fatalf("expected 3 gap-filled hour buckets, got %d", len(res.Buckets))
	}
	for _, b := range res.Buckets {
		if b.RequestCount != 0 || b.ErrorCount != 0 || b.InputTokensTotal != 0 {
			t.Fatalf("empty bucket should be zeroed: %+v", b)
		}
	}
	s := res.Summary
	if s.TotalRequests != 0 || s.TotalErrors != 0 || s.ErrorRate != 0 ||
		s.TotalInputTokens != 0 || s.TotalOutputTokens != 0 {
		t.Fatalf("empty summary should be zeroed: %+v", s)
	}
}

// TestRequestMetrics_MixedBucketsAndGapFill seeds 200/500 traffic across two
// non-adjacent hour buckets and asserts per-bucket counts, error counts, that
// the empty middle bucket is gap-filled to zero, and the window summary totals +
// errorRate.
func TestRequestMetrics_MixedBucketsAndGapFill(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	from := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	to := from.Add(3 * time.Hour) // buckets: 10:00, 11:00, 12:00

	// Bucket 10:00 — 2 ok + 1 error (status 500).
	seedRequestLog(t, r.Ent, from.Add(5*time.Minute), 200, 100, 10, 20)
	seedRequestLog(t, r.Ent, from.Add(15*time.Minute), 200, 200, 10, 20)
	seedRequestLog(t, r.Ent, from.Add(25*time.Minute), 500, 300, 10, 20)
	// Bucket 11:00 — intentionally empty (gap-fill target).
	// Bucket 12:00 — 1 ok + 1 error (status 404, also >= 400).
	seedRequestLog(t, r.Ent, from.Add(2*time.Hour+10*time.Minute), 200, 50, 5, 7)
	seedRequestLog(t, r.Ent, from.Add(2*time.Hour+20*time.Minute), 404, 60, 5, 7)

	res, err := qr.RequestMetrics(context.Background(), from, to, model.RequestMetricsBucketGranularityHour, nil)
	if err != nil {
		t.Fatalf("RequestMetrics: %v", err)
	}
	if len(res.Buckets) != 3 {
		t.Fatalf("expected 3 buckets, got %d: %+v", len(res.Buckets), res.Buckets)
	}

	b0, b1, b2 := res.Buckets[0], res.Buckets[1], res.Buckets[2]
	// Chronological order.
	if !b0.Timestamp.Equal(from) || !b1.Timestamp.Equal(from.Add(time.Hour)) || !b2.Timestamp.Equal(from.Add(2*time.Hour)) {
		t.Fatalf("buckets not chronological: %v %v %v", b0.Timestamp, b1.Timestamp, b2.Timestamp)
	}
	// Bucket 10:00.
	if b0.RequestCount != 3 || b0.ErrorCount != 1 {
		t.Fatalf("bucket0 wrong: req=%d err=%d", b0.RequestCount, b0.ErrorCount)
	}
	if b0.InputTokensTotal != 30 || b0.OutputTokensTotal != 60 {
		t.Fatalf("bucket0 tokens wrong: in=%d out=%d", b0.InputTokensTotal, b0.OutputTokensTotal)
	}
	// Bucket 11:00 — gap-filled to zero.
	if b1.RequestCount != 0 || b1.ErrorCount != 0 || b1.InputTokensTotal != 0 {
		t.Fatalf("middle bucket should be zero-filled: %+v", b1)
	}
	// Bucket 12:00.
	if b2.RequestCount != 2 || b2.ErrorCount != 1 {
		t.Fatalf("bucket2 wrong: req=%d err=%d", b2.RequestCount, b2.ErrorCount)
	}

	// Summary: 5 requests, 2 errors → errorRate 0.4; tokens summed window-wide.
	s := res.Summary
	if s.TotalRequests != 5 || s.TotalErrors != 2 {
		t.Fatalf("summary totals wrong: req=%d err=%d", s.TotalRequests, s.TotalErrors)
	}
	if s.ErrorRate < 0.399 || s.ErrorRate > 0.401 {
		t.Fatalf("errorRate = %v, want ~0.4", s.ErrorRate)
	}
	if s.TotalInputTokens != 40 || s.TotalOutputTokens != 74 {
		t.Fatalf("summary tokens wrong: in=%d out=%d", s.TotalInputTokens, s.TotalOutputTokens)
	}
	if s.AvgLatencyMs <= 0 || s.P95LatencyMs <= 0 {
		t.Fatalf("latency summary should be positive with traffic: avg=%d p95=%d", s.AvgLatencyMs, s.P95LatencyMs)
	}
}

// TestRequestMetrics_FilterAndValidation covers the status-code filter (only
// matching rows aggregate) and the from>=to validation error.
func TestRequestMetrics_FilterAndValidation(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}
	ctx := context.Background()

	from := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	seedRequestLog(t, r.Ent, from.Add(5*time.Minute), 200, 100, 1, 1)
	seedRequestLog(t, r.Ent, from.Add(6*time.Minute), 500, 100, 1, 1)

	// Filter to status 500 → only the one error row aggregates.
	status := 500
	res, err := qr.RequestMetrics(ctx, from, to, model.RequestMetricsBucketGranularityHour,
		&model.RequestMetricsFilter{StatusCode: &status})
	if err != nil {
		t.Fatalf("filtered RequestMetrics: %v", err)
	}
	if res.Summary.TotalRequests != 1 || res.Summary.TotalErrors != 1 {
		t.Fatalf("status filter wrong: req=%d err=%d", res.Summary.TotalRequests, res.Summary.TotalErrors)
	}
	if res.Summary.ErrorRate != 1.0 {
		t.Fatalf("errorRate = %v, want 1.0", res.Summary.ErrorRate)
	}

	// Validation: from == to and from > to both rejected.
	if _, err := qr.RequestMetrics(ctx, to, to, model.RequestMetricsBucketGranularityHour, nil); err == nil {
		t.Fatal("from == to should be rejected")
	}
	if _, err := qr.RequestMetrics(ctx, to, from, model.RequestMetricsBucketGranularityHour, nil); err == nil {
		t.Fatal("from > to should be rejected")
	}
}

// TestRequestMetrics_RunawayGuard rejects a window/granularity pair that would
// exceed the bucket cap before any DB work.
func TestRequestMetrics_RunawayGuard(t *testing.T) {
	r, cleanup := newTestResolver(t)
	defer cleanup()
	qr := &queryResolver{r}

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 30) // 30 days of MINUTE buckets ≫ 1500
	if _, err := qr.RequestMetrics(context.Background(), from, to, model.RequestMetricsBucketGranularityMinute, nil); err == nil {
		t.Fatal("oversized minute window should be rejected")
	}
}
