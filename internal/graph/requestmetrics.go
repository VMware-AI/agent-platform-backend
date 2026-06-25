package graph

// Windowed real-time request-traffic metrics (模块 实时监控). Aggregates RequestLog
// over a [from, to) window, bucketed by minute/hour/day, with pushed-down SQL
// (ent .Modify + raw sql.Selector) so raw rows are never materialized — the same
// idiom as metering.resolvers.go's byDay block. The aggregation is dialect-aware:
// Postgres (prod) uses date_trunc + percentile_cont + count(*) filter exactly as
// designed; sqlite (dev/test) uses portable equivalents so the resolver runs on
// the in-memory test harness. Both dialects run the DB in UTC, so bucket
// timestamps line up with stored UTC created_at values.

import (
	"context"
	"fmt"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/VMware-AI/agent-platform-backend/ent"
	"github.com/VMware-AI/agent-platform-backend/ent/agent"
	"github.com/VMware-AI/agent-platform-backend/ent/requestlog"
	"github.com/VMware-AI/agent-platform-backend/internal/graph/model"
	"github.com/google/uuid"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

// maxRequestMetricsBuckets guards against a runaway window/granularity pairing
// (e.g. a year of minute buckets) that would explode the gap-fill slice and the
// payload. ~1500 buckets ≈ 25h of minutes, 62 days of hours, or 4 years of days.
const maxRequestMetricsBuckets = 1500

// metricRow is the scanned shape of one bucketed aggregation row. The bucket key
// is read as text (cast in SQL) so a single Go parser handles both dialects.
type metricRow struct {
	Bucket     string `json:"bucket"`
	Requests   int    `json:"requests"`
	Errors     int    `json:"errors"`
	AvgLatency int    `json:"avg_latency"`
	P95Latency int    `json:"p95_latency"`
	InTokens   int    `json:"in_tokens"`
	OutTokens  int    `json:"out_tokens"`
}

// requestMetrics is the real implementation behind the RequestMetrics resolver.
func (r *queryResolver) requestMetrics(ctx context.Context, from time.Time, to time.Time, granularity model.RequestMetricsBucketGranularity, filter *model.RequestMetricsFilter) (*model.RequestMetrics, error) {
	// Validation (fail fast) — bound the window and guard runaway bucket counts.
	if !from.Before(to) {
		return nil, gqlerror.Errorf("from must be before to")
	}
	if !granularity.IsValid() {
		return nil, gqlerror.Errorf("invalid granularity")
	}
	if expectedBucketCount(from, to, granularity) > maxRequestMetricsBuckets {
		return nil, gqlerror.Errorf("time window too large for the chosen granularity")
	}

	// Base query: filter → time window → tenant scope. Built once and Cloned for
	// the per-bucket and window-summary passes so the WHERE clause is identical.
	base, err := r.requestMetricsBaseQuery(ctx, from, to, filter)
	if err != nil {
		return nil, err
	}

	rows, err := scanBucketedMetrics(ctx, base.Clone(), granularity)
	if err != nil {
		return nil, err
	}
	summary, err := scanSummaryMetrics(ctx, base.Clone())
	if err != nil {
		return nil, err
	}

	buckets := gapFillBuckets(from, to, granularity, rows)
	return &model.RequestMetrics{
		RangeStart:  from,
		RangeEnd:    to,
		Granularity: granularity,
		Buckets:     buckets,
		Summary:     summary,
	}, nil
}

// requestMetricsBaseQuery applies the optional filter, the [from, to) window, and
// tenant isolation. The tenant block is copied verbatim from the RequestLogs
// resolver so list and metrics views show the same traffic to a tenant-admin.
func (r *queryResolver) requestMetricsBaseQuery(ctx context.Context, from, to time.Time, filter *model.RequestMetricsFilter) (*ent.RequestLogQuery, error) {
	q := r.Ent.RequestLog.Query()
	if filter != nil {
		if filter.StatusCode != nil {
			q = q.Where(requestlog.StatusCode(*filter.StatusCode))
		}
		if filter.AgentID != nil {
			aid, err := uuid.Parse(*filter.AgentID)
			if err != nil {
				return nil, gqlerror.Errorf("invalid agentId")
			}
			q = q.Where(requestlog.AgentID(aid))
		}
		if filter.Model != nil {
			q = q.Where(requestlog.Model(*filter.Model))
		}
	}
	q = q.Where(requestlog.CreatedAtGTE(from), requestlog.CreatedAtLT(to))

	// Tenant isolation (LLD-10 B-class): a request log belongs to its user's /
	// agent's tenant; a tenant-admin sees only their tenant's traffic. Copied from
	// the RequestLogs resolver so behavior matches exactly.
	if d := tenantScopeFor(ctx); d.apply {
		if d.denyAll {
			q = q.Where(requestlog.IDEQ(uuid.Nil))
		} else {
			uids, err := r.tenantMemberIDs(ctx, d.tenant)
			if err != nil {
				return nil, err
			}
			aids, err := r.Ent.Agent.Query().Where(agent.TenantID(d.tenant)).IDs(ctx)
			if err != nil {
				return nil, err
			}
			q = q.Where(requestlog.Or(requestlog.UserIDIn(uids...), requestlog.AgentIDIn(aids...)))
		}
	}
	return q, nil
}

// bucketExpr returns the dialect-appropriate SQL expression that truncates
// created_at to the granularity, casting the result to a canonical text key
// ("YYYY-MM-DD HH:MM:SS") parsed back by normalizeBucketKey + matched against
// bucketKey during gap-fill. Postgres truncates the timestamp explicitly AT TIME
// ZONE 'UTC' so the bucket boundary (and its text key) is the UTC wall clock
// regardless of the DB session TimeZone — otherwise a non-UTC session would emit
// local-zone keys that never match the UTC bucketKey, silently zeroing every
// bucket. sqlite truncates by string-prefix because the modernc sqlite driver
// stores time.Time in Go's "... +0000 UTC" layout, which sqlite's date/strftime
// functions cannot parse (they silently return ”). The text prefix is always
// "YYYY-MM-DD HH:MM:SS", so substr + concatenation yields the same canonical key
// the Postgres path produces.
func bucketExpr(s *sql.Selector, g model.RequestMetricsBucketGranularity) string {
	col := s.C(requestlog.FieldCreatedAt)
	if s.Dialect() == dialect.Postgres {
		unit := postgresTruncUnit(g)
		return fmt.Sprintf("cast(date_trunc('%s', %s at time zone 'UTC') as text)", unit, col)
	}
	text := fmt.Sprintf("cast(%s as text)", col)
	switch g {
	case model.RequestMetricsBucketGranularityMinute:
		return fmt.Sprintf("(substr(%s,1,16) || ':00')", text)
	case model.RequestMetricsBucketGranularityHour:
		return fmt.Sprintf("(substr(%s,1,13) || ':00:00')", text)
	default:
		return fmt.Sprintf("(substr(%s,1,10) || ' 00:00:00')", text)
	}
}

func postgresTruncUnit(g model.RequestMetricsBucketGranularity) string {
	switch g {
	case model.RequestMetricsBucketGranularityMinute:
		return "minute"
	case model.RequestMetricsBucketGranularityHour:
		return "hour"
	default:
		return "day"
	}
}

// errorFilterExpr counts rows with status_code >= 400. Postgres uses the
// FILTER clause; sqlite uses count(case when ...).
func errorFilterExpr(s *sql.Selector) string {
	col := s.C(requestlog.FieldStatusCode)
	if s.Dialect() == dialect.Postgres {
		return fmt.Sprintf("count(*) filter (where %s >= 400)", col)
	}
	return fmt.Sprintf("count(case when %s >= 400 then 1 end)", col)
}

// p95Expr returns the 95th-percentile latency expression. Postgres uses
// percentile_cont within group (exact). sqlite has no percentile aggregate, so
// it falls back to max(latency_ms) as a conservative upper-bound stand-in (tests
// assert presence/ordering, not the exact percentile value on sqlite).
func p95Expr(s *sql.Selector) string {
	col := s.C(requestlog.FieldLatencyMs)
	if s.Dialect() == dialect.Postgres {
		return fmt.Sprintf("coalesce(percentile_cont(0.95) within group (order by %s),0)::int", col)
	}
	return fmt.Sprintf("cast(coalesce(max(%s),0) as integer)", col)
}

// avgExpr returns the average-latency expression rounded to an int in both
// dialects (Postgres ::int, sqlite cast as integer).
func avgExpr(s *sql.Selector) string {
	col := s.C(requestlog.FieldLatencyMs)
	if s.Dialect() == dialect.Postgres {
		return fmt.Sprintf("coalesce(avg(%s),0)::int", col)
	}
	return fmt.Sprintf("cast(coalesce(avg(%s),0) as integer)", col)
}

// scanBucketedMetrics runs the per-bucket aggregation, pushed down via .Modify.
func scanBucketedMetrics(ctx context.Context, q *ent.RequestLogQuery, g model.RequestMetricsBucketGranularity) ([]metricRow, error) {
	var rows []metricRow
	err := q.Modify(func(s *sql.Selector) {
		bucket := bucketExpr(s, g)
		s.Select(
			sql.As(bucket, "bucket"),
			sql.As("count(*)", "requests"),
			sql.As(errorFilterExpr(s), "errors"),
			sql.As(avgExpr(s), "avg_latency"),
			sql.As(p95Expr(s), "p95_latency"),
			sql.As(fmt.Sprintf("coalesce(sum(%s),0)", s.C(requestlog.FieldInputTokens)), "in_tokens"),
			sql.As(fmt.Sprintf("coalesce(sum(%s),0)", s.C(requestlog.FieldOutputTokens)), "out_tokens"),
		).GroupBy(bucket).OrderBy(sql.Asc("bucket"))
	}).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// scanSummaryMetrics computes window-wide totals with the same WHERE clause.
func scanSummaryMetrics(ctx context.Context, q *ent.RequestLogQuery) (*model.RequestMetricsSummary, error) {
	var agg []struct {
		Requests   int `json:"requests"`
		Errors     int `json:"errors"`
		AvgLatency int `json:"avg_latency"`
		P95Latency int `json:"p95_latency"`
		InTokens   int `json:"in_tokens"`
		OutTokens  int `json:"out_tokens"`
	}
	err := q.Modify(func(s *sql.Selector) {
		s.Select(
			sql.As("count(*)", "requests"),
			sql.As(errorFilterExpr(s), "errors"),
			sql.As(avgExpr(s), "avg_latency"),
			sql.As(p95Expr(s), "p95_latency"),
			sql.As(fmt.Sprintf("coalesce(sum(%s),0)", s.C(requestlog.FieldInputTokens)), "in_tokens"),
			sql.As(fmt.Sprintf("coalesce(sum(%s),0)", s.C(requestlog.FieldOutputTokens)), "out_tokens"),
		)
	}).Scan(ctx, &agg)
	if err != nil {
		return nil, err
	}
	out := &model.RequestMetricsSummary{}
	if len(agg) > 0 {
		a := agg[0]
		out.TotalRequests = a.Requests
		out.TotalErrors = a.Errors
		out.AvgLatencyMs = a.AvgLatency
		out.P95LatencyMs = a.P95Latency
		out.TotalInputTokens = a.InTokens
		out.TotalOutputTokens = a.OutTokens
	}
	// errorRate = errors/requests, guarding divide-by-zero (0.0 for empty window).
	if out.TotalRequests > 0 {
		out.ErrorRate = float64(out.TotalErrors) / float64(out.TotalRequests)
	}
	return out, nil
}

// gapFillBuckets generates every bucket from truncate(from) stepping by the
// granularity up to (exclusive) to, zero-filling buckets with no rows, and maps
// scanned DB rows in by their truncated-timestamp key. Returns chronological.
func gapFillBuckets(from, to time.Time, g model.RequestMetricsBucketGranularity, rows []metricRow) []model.RequestMetricsBucket {
	byKey := make(map[string]metricRow, len(rows))
	for _, row := range rows {
		byKey[normalizeBucketKey(row.Bucket)] = row
	}
	out := make([]model.RequestMetricsBucket, 0, len(rows)+1)
	for ts := truncateTo(from, g); ts.Before(to); ts = stepBucket(ts, g) {
		b := model.RequestMetricsBucket{Timestamp: ts}
		if row, ok := byKey[bucketKey(ts)]; ok {
			b.RequestCount = row.Requests
			b.ErrorCount = row.Errors
			b.AvgLatencyMs = row.AvgLatency
			b.P95LatencyMs = row.P95Latency
			b.InputTokensTotal = row.InTokens
			b.OutputTokensTotal = row.OutTokens
		}
		out = append(out, b)
	}
	return out
}

// bucketKey renders a UTC timestamp as the canonical text key both dialects
// produce ("YYYY-MM-DD HH:MM:SS"), used to join DB rows to generated buckets.
func bucketKey(ts time.Time) string {
	return ts.UTC().Format("2006-01-02 15:04:05")
}

// normalizeBucketKey canonicalizes a DB-produced bucket string to "YYYY-MM-DD
// HH:MM:SS". Postgres date_trunc cast-to-text yields e.g. "2026-06-25 13:00:00"
// (sometimes with a "+00" offset or fractional seconds); sqlite strftime already
// matches. We trim to the first 19 chars (date + space + HH:MM:SS).
func normalizeBucketKey(raw string) string {
	if len(raw) >= 19 {
		return raw[:19]
	}
	return raw
}

// truncateTo floors a timestamp to the start of its granularity bucket (UTC).
func truncateTo(ts time.Time, g model.RequestMetricsBucketGranularity) time.Time {
	t := ts.UTC()
	switch g {
	case model.RequestMetricsBucketGranularityMinute:
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
	case model.RequestMetricsBucketGranularityHour:
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, time.UTC)
	default:
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	}
}

// stepBucket advances one bucket. DAY uses AddDate for calendar correctness
// (DST-agnostic in UTC, but the right idiom regardless).
func stepBucket(ts time.Time, g model.RequestMetricsBucketGranularity) time.Time {
	switch g {
	case model.RequestMetricsBucketGranularityMinute:
		return ts.Add(time.Minute)
	case model.RequestMetricsBucketGranularityHour:
		return ts.Add(time.Hour)
	default:
		return ts.AddDate(0, 0, 1)
	}
}

// expectedBucketCount estimates how many buckets a [from, to) window spans at the
// given granularity, used by the runaway guard before any DB work.
func expectedBucketCount(from, to time.Time, g model.RequestMetricsBucketGranularity) int {
	span := to.Sub(truncateTo(from, g))
	switch g {
	case model.RequestMetricsBucketGranularityMinute:
		return int(span/time.Minute) + 1
	case model.RequestMetricsBucketGranularityHour:
		return int(span/time.Hour) + 1
	default:
		return int(span/(24*time.Hour)) + 1
	}
}
