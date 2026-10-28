package store

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
)

// MonitorOpts controls GET .../monitor aggregation.
type MonitorOpts struct {
	Project  string // session name or UUID
	Window   string // 1h|24h|7d
	From, To *time.Time
	Limit    int // by_model / by_tool row cap; default 50
}

type monitorBucketSpec struct {
	Window string
	Bucket string // "1m"|"1h"|"6h"
	Dur    time.Duration
	Points int
}

func monitorBucketForWindow(window string) (monitorBucketSpec, error) {
	switch window {
	case "", "24h":
		return monitorBucketSpec{Window: "24h", Bucket: "1h", Dur: time.Hour, Points: 24}, nil
	case "1h":
		return monitorBucketSpec{Window: "1h", Bucket: "1m", Dur: time.Minute, Points: 60}, nil
	case "7d":
		return monitorBucketSpec{Window: "7d", Bucket: "6h", Dur: 6 * time.Hour, Points: 28}, nil
	default:
		return monitorBucketSpec{}, fmt.Errorf("window must be 1h|24h|7d")
	}
}

// alignBucketFloor truncates t down to a bucket boundary in UTC.
func alignBucketFloor(t time.Time, dur time.Duration) time.Time {
	t = t.UTC()
	switch dur {
	case time.Minute:
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
	case time.Hour:
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, time.UTC)
	case 6 * time.Hour:
		h := t.Hour() - (t.Hour() % 6)
		return time.Date(t.Year(), t.Month(), t.Day(), h, 0, 0, 0, time.UTC)
	default:
		// generic: align via unix nano
		ns := t.UnixNano()
		d := dur.Nanoseconds()
		if d <= 0 {
			return t
		}
		return time.Unix(0, ns-ns%d).UTC()
	}
}

// monitorWindowBounds returns aligned [from,to) covering exactly spec.Points buckets
// ending at the latest complete-or-current bucket floor of now (or custom from/to).
func monitorWindowBounds(spec monitorBucketSpec, from, to *time.Time) (time.Time, time.Time, error) {
	if from != nil && to != nil {
		f := alignBucketFloor(from.UTC(), spec.Dur)
		t := alignBucketFloor(to.UTC(), spec.Dur)
		if !to.UTC().Equal(t) {
			// include the bucket that contains `to` only if to is exclusive past a boundary;
			// half-open [from,to): advance t to next boundary if to is not aligned.
			t = t.Add(spec.Dur)
		}
		if !t.After(f) {
			return time.Time{}, time.Time{}, fmt.Errorf("to must be after from")
		}
		return f, t, nil
	}
	now := time.Now().UTC()
	end := alignBucketFloor(now, spec.Dur)
	// Include the in-progress bucket that contains `now`.
	end = end.Add(spec.Dur)
	start := end.Add(-time.Duration(spec.Points) * spec.Dur)
	return start, end, nil
}

func bucketStarts(from, to time.Time, dur time.Duration) []time.Time {
	var out []time.Time
	for t := from; t.Before(to); t = t.Add(dur) {
		out = append(out, t)
	}
	return out
}

func monitorETag(projectID, window string, from, to time.Time, traceCount, errorCount int, lastBucket string) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s|%s|%s|%s|%d|%d|%s",
		projectID, window,
		from.UTC().Format(time.RFC3339Nano),
		to.UTC().Format(time.RFC3339Nano),
		traceCount, errorCount, lastBucket)
	return `"` + hex.EncodeToString(h.Sum(nil)) + `"`
}

// UIMonitor builds the Monitor board payload for a project window.
func (s *Store) UIMonitor(ctx context.Context, opts MonitorOpts) (map[string]any, string, error) {
	spec, err := monitorBucketForWindow(opts.Window)
	if err != nil {
		return nil, "", err
	}
	from, to, err := monitorWindowBounds(spec, opts.From, opts.To)
	if err != nil {
		return nil, "", err
	}
	if opts.Limit <= 0 || opts.Limit > 200 {
		opts.Limit = 50
	}

	sessionID, err := s.LookupSessionID(ctx, opts.Project)
	if err != nil {
		return nil, "", err
	}
	if sessionID == uuid.Nil {
		return nil, "", ErrProjectNotFound
	}
	var projectName string
	if err := s.pool.QueryRow(ctx, `SELECT name FROM sessions WHERE id=$1`, sessionID).Scan(&projectName); err != nil {
		return nil, "", err
	}

	type rootRow struct {
		traceID uuid.UUID
		start   time.Time
		end     *time.Time
		err     *string
	}
	rows, err := s.pool.Query(ctx, `
		SELECT trace_id, start_time, end_time, error
		FROM runs
		WHERE session_id=$1 AND parent_run_id IS NULL AND trace_id IS NOT NULL
			AND start_time >= $2 AND start_time < $3`, sessionID, from, to)
	if err != nil {
		return nil, "", err
	}
	var roots []rootRow
	for rows.Next() {
		var r rootRow
		if err := rows.Scan(&r.traceID, &r.start, &r.end, &r.err); err != nil {
			rows.Close()
			return nil, "", err
		}
		roots = append(roots, r)
	}
	rows.Close()

	starts := bucketStarts(from, to, spec.Dur)
	type bAcc struct {
		success, errN, total int
		lats                 []float64
		in, out, tot         int64
	}
	acc := make([]bAcc, len(starts))
	bucketIndex := func(t time.Time) int {
		if t.Before(from) || !t.Before(to) {
			return -1
		}
		return int(t.Sub(from) / spec.Dur)
	}

	traceCount := len(roots)
	successCount, errorCount := 0, 0
	var headerLats []float64
	for _, r := range roots {
		bi := bucketIndex(r.start)
		isErr := r.err != nil && *r.err != ""
		if isErr {
			errorCount++
		} else if r.end != nil {
			successCount++
		}
		if r.end != nil {
			ms := float64(r.end.Sub(r.start).Milliseconds())
			headerLats = append(headerLats, ms)
			if bi >= 0 && bi < len(acc) {
				acc[bi].lats = append(acc[bi].lats, ms)
			}
		}
		if bi >= 0 && bi < len(acc) {
			acc[bi].total++
			if isErr {
				acc[bi].errN++
			} else if r.end != nil {
				acc[bi].success++
			}
		}
	}

	// Tokens: all runs belonging to roots that start in the window, attributed to the root's bucket.
	tokRows, err := s.pool.Query(ctx, `
		SELECT r.trace_id, r.start_time,
			COALESCE(SUM(c.input_tokens),0), COALESCE(SUM(c.output_tokens),0), COALESCE(SUM(c.total_tokens),0)
		FROM runs r
		JOIN runs c ON c.trace_id = r.trace_id
		WHERE r.session_id=$1 AND r.parent_run_id IS NULL AND r.trace_id IS NOT NULL
			AND r.start_time >= $2 AND r.start_time < $3
		GROUP BY r.trace_id, r.start_time`, sessionID, from, to)
	if err != nil {
		return nil, "", err
	}
	var tokIn, tokOut, tokTot int64
	for tokRows.Next() {
		var tid uuid.UUID
		var start time.Time
		var in, out, tot int64
		if err := tokRows.Scan(&tid, &start, &in, &out, &tot); err != nil {
			tokRows.Close()
			return nil, "", err
		}
		tokIn += in
		tokOut += out
		tokTot += tot
		bi := bucketIndex(start)
		if bi >= 0 && bi < len(acc) {
			acc[bi].in += in
			acc[bi].out += out
			acc[bi].tot += tot
		}
	}
	tokRows.Close()

	seriesT := make([]map[string]any, 0, len(starts))
	seriesL := make([]map[string]any, 0, len(starts))
	seriesTok := make([]map[string]any, 0, len(starts))
	for i, t0 := range starts {
		t1 := t0.Add(spec.Dur)
		a := acc[i]
		seriesT = append(seriesT, map[string]any{
			"t": t0.Format(time.RFC3339Nano), "t_end": t1.Format(time.RFC3339Nano),
			"success": a.success, "error": a.errN, "total": a.total,
		})
		var p50, p99 any
		if len(a.lats) > 0 {
			sort.Float64s(a.lats)
			p50 = percentile(a.lats, 0.50)
			p99 = percentile(a.lats, 0.99)
		}
		seriesL = append(seriesL, map[string]any{
			"t": t0.Format(time.RFC3339Nano), "t_end": t1.Format(time.RFC3339Nano),
			"p50_ms": p50, "p99_ms": p99,
		})
		seriesTok = append(seriesTok, map[string]any{
			"t": t0.Format(time.RFC3339Nano), "t_end": t1.Format(time.RFC3339Nano),
			"input": a.in, "output": a.out, "total": a.tot,
		})
	}

	byModel, err := s.monitorByModel(ctx, sessionID, from, to, opts.Limit)
	if err != nil {
		return nil, "", err
	}
	byTool, err := s.monitorByTool(ctx, sessionID, from, to, opts.Limit)
	if err != nil {
		return nil, "", err
	}

	var headerP50, headerP99 any
	if len(headerLats) > 0 {
		sort.Float64s(headerLats)
		headerP50 = percentile(headerLats, 0.50)
		headerP99 = percentile(headerLats, 0.99)
	}
	lastBucket := ""
	if len(starts) > 0 {
		lastBucket = starts[len(starts)-1].Format(time.RFC3339Nano)
	}
	etag := monitorETag(sessionID.String(), spec.Window, from, to, traceCount, errorCount, lastBucket)

	out := map[string]any{
		"project_id":     sessionID.String(),
		"project":        projectName,
		"window":         spec.Window,
		"from":           from.Format(time.RFC3339Nano),
		"to":             to.Format(time.RFC3339Nano),
		"bucket":         spec.Bucket,
		"trace_count":    traceCount,
		"success_count":  successCount,
		"error_count":    errorCount,
		"p50_ms":         headerP50,
		"p99_ms":         headerP99,
		"tokens":         map[string]any{"input": tokIn, "output": tokOut, "total": tokTot},
		"cost":           nil,
		"series_traces":  seriesT,
		"series_latency": seriesL,
		"series_tokens":  seriesTok,
		"by_model":       byModel,
		"by_tool":        byTool,
	}
	return out, etag, nil
}

func zeroSeries(starts []time.Time, dur time.Duration) ([]map[string]any, []map[string]any, []map[string]any) {
	seriesT := make([]map[string]any, 0, len(starts))
	seriesL := make([]map[string]any, 0, len(starts))
	seriesTok := make([]map[string]any, 0, len(starts))
	for _, t0 := range starts {
		t1 := t0.Add(dur)
		ts, te := t0.Format(time.RFC3339Nano), t1.Format(time.RFC3339Nano)
		seriesT = append(seriesT, map[string]any{"t": ts, "t_end": te, "success": 0, "error": 0, "total": 0})
		seriesL = append(seriesL, map[string]any{"t": ts, "t_end": te, "p50_ms": nil, "p99_ms": nil})
		seriesTok = append(seriesTok, map[string]any{"t": ts, "t_end": te, "input": int64(0), "output": int64(0), "total": int64(0)})
	}
	return seriesT, seriesL, seriesTok
}

func (s *Store) monitorByModel(ctx context.Context, sessionID uuid.UUID, from, to time.Time, limit int) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(NULLIF(c.model, ''), '(unknown)') AS model,
			COUNT(*)::int AS calls,
			COUNT(*) FILTER (WHERE c.error IS NOT NULL AND c.error <> '')::int AS errors,
			COALESCE(SUM(c.input_tokens),0)::bigint,
			COALESCE(SUM(c.output_tokens),0)::bigint,
			COALESCE(SUM(c.total_tokens),0)::bigint,
			percentile_cont(0.50) WITHIN GROUP (
				ORDER BY EXTRACT(EPOCH FROM (c.end_time - c.start_time)) * 1000)
				FILTER (WHERE c.end_time IS NOT NULL AND c.start_time IS NOT NULL) AS p50,
			percentile_cont(0.99) WITHIN GROUP (
				ORDER BY EXTRACT(EPOCH FROM (c.end_time - c.start_time)) * 1000)
				FILTER (WHERE c.end_time IS NOT NULL AND c.start_time IS NOT NULL) AS p99
		FROM runs c
		INNER JOIN runs r ON r.trace_id = c.trace_id AND r.parent_run_id IS NULL
		WHERE r.session_id=$1 AND r.start_time >= $2 AND r.start_time < $3
			AND c.run_type = 'llm'
		GROUP BY 1
		ORDER BY calls DESC, model ASC
		LIMIT $4`, sessionID, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type row struct {
		model        string
		calls, errs  int
		in, out, tot int64
		p50, p99     *float64
	}
	var list []row
	var totalCalls int
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.model, &r.calls, &r.errs, &r.in, &r.out, &r.tot, &r.p50, &r.p99); err != nil {
			return nil, err
		}
		totalCalls += r.calls
		list = append(list, r)
	}
	out := make([]map[string]any, 0, len(list))
	for _, r := range list {
		share := 0.0
		if totalCalls > 0 {
			share = float64(r.calls) / float64(totalCalls)
		}
		errRate := 0.0
		if r.calls > 0 {
			errRate = float64(r.errs) / float64(r.calls)
		}
		var p50, p99 any
		if r.p50 != nil {
			p50 = int(math.Round(*r.p50))
		}
		if r.p99 != nil {
			p99 = int(math.Round(*r.p99))
		}
		out = append(out, map[string]any{
			"model": r.model, "calls": r.calls, "share": share,
			"error_count": r.errs, "error_rate": errRate,
			"p50_ms": p50, "p99_ms": p99,
			"tokens": map[string]any{"input": r.in, "output": r.out, "total": r.tot},
		})
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out, nil
}

func (s *Store) monitorByTool(ctx context.Context, sessionID uuid.UUID, from, to time.Time, limit int) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(NULLIF(c.name, ''), '(unknown)') AS tool,
			COUNT(*)::int AS calls,
			COUNT(*) FILTER (WHERE c.error IS NOT NULL AND c.error <> '')::int AS errors,
			COALESCE(SUM(c.total_tokens),0)::bigint AS tok,
			percentile_cont(0.50) WITHIN GROUP (
				ORDER BY EXTRACT(EPOCH FROM (c.end_time - c.start_time)) * 1000)
				FILTER (WHERE c.end_time IS NOT NULL AND c.start_time IS NOT NULL) AS p50,
			percentile_cont(0.99) WITHIN GROUP (
				ORDER BY EXTRACT(EPOCH FROM (c.end_time - c.start_time)) * 1000)
				FILTER (WHERE c.end_time IS NOT NULL AND c.start_time IS NOT NULL) AS p99
		FROM runs c
		INNER JOIN runs r ON r.trace_id = c.trace_id AND r.parent_run_id IS NULL
		WHERE r.session_id=$1 AND r.start_time >= $2 AND r.start_time < $3
			AND c.run_type = 'tool'
		GROUP BY 1
		ORDER BY calls DESC, tool ASC
		LIMIT $4`, sessionID, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type row struct {
		tool        string
		calls, errs int
		tok         int64
		p50, p99    *float64
	}
	var list []row
	var totalCalls int
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.tool, &r.calls, &r.errs, &r.tok, &r.p50, &r.p99); err != nil {
			return nil, err
		}
		totalCalls += r.calls
		list = append(list, r)
	}
	out := make([]map[string]any, 0, len(list))
	for _, r := range list {
		share := 0.0
		if totalCalls > 0 {
			share = float64(r.calls) / float64(totalCalls)
		}
		errRate := 0.0
		if r.calls > 0 {
			errRate = float64(r.errs) / float64(r.calls)
		}
		var p50, p99 any
		if r.p50 != nil {
			p50 = int(math.Round(*r.p50))
		}
		if r.p99 != nil {
			p99 = int(math.Round(*r.p99))
		}
		var tokens any
		if r.tok > 0 {
			tokens = map[string]any{"input": 0, "output": 0, "total": r.tok}
		} else {
			tokens = nil
		}
		out = append(out, map[string]any{
			"tool": r.tool, "calls": r.calls, "share": share,
			"error_count": r.errs, "error_rate": errRate,
			"p50_ms": p50, "p99_ms": p99,
			"tokens": tokens,
		})
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out, nil
}
