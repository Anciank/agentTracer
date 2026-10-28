package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Anciank/agentTracer/server/internal/traces"
)

// resolveThreadKey reads conversation grouping from extra.metadata on a run.
// Precedence: metadata.thread_id, then metadata.session_id.
// Never uses runs.session_id (that column is the LangSmith project).
// Returns (nil, nil) when neither key is a non-empty string.
func resolveThreadKey(merged map[string]any) (threadID *string, source *string) {
	meta := digExtraMetadata(merged["extra"])
	if meta == nil {
		return nil, nil
	}
	if v := strings.TrimSpace(asString(meta["thread_id"])); v != "" {
		src := "thread_id"
		return &v, &src
	}
	if v := strings.TrimSpace(asString(meta["session_id"])); v != "" {
		src := "session_id"
		return &v, &src
	}
	return nil, nil
}

// ThreadListOpts controls GET /api/ui/threads.
type ThreadListOpts struct {
	Project     string
	Limit       int
	CursorStart *time.Time
	CursorID    string // thread_id keyset
}

// UIListThreads groups root runs by denormalized thread_id.
// Unthreaded roots (thread_id IS NULL) are hidden.
// status is derived from the latest root turn only; error_count counts failed roots
// (any-run-error) across the whole thread.
func (s *Store) UIListThreads(ctx context.Context, opts ThreadListOpts) ([]map[string]any, string, error) {
	if opts.Limit <= 0 || opts.Limit > 200 {
		opts.Limit = 50
	}
	args := []any{}
	n := 1
	where := []string{
		"r.parent_run_id IS NULL",
		"r.thread_id IS NOT NULL",
		"r.trace_id IS NOT NULL",
	}
	if opts.Project != "" {
		where = append(where, fmt.Sprintf("s.name=$%d", n))
		args = append(args, opts.Project)
		n++
	}

	curClause := ""
	if opts.CursorStart != nil && opts.CursorID != "" {
		curClause = fmt.Sprintf(
			`WHERE (a.last_activity, a.thread_id) < ($%d::timestamptz, $%d::text)`, n, n+1)
		args = append(args, *opts.CursorStart, opts.CursorID)
		n += 2
	}

	q := fmt.Sprintf(`
		WITH roots AS (
			SELECT r.thread_id, r.thread_key_source, r.trace_id, r.start_time, r.end_time,
				r.status, r.error, r.first_user_preview, r.last_ai_preview, s.name AS project
			FROM runs r
			LEFT JOIN sessions s ON s.id = r.session_id
			WHERE %s
		),
		agg AS (
			SELECT thread_id,
				COUNT(*)::int AS turn_count,
				MIN(start_time) AS first_activity,
				MAX(start_time) AS last_activity,
				COUNT(*) FILTER (WHERE %s)::int AS error_count
			FROM roots
			GROUP BY thread_id
		),
		latest AS (
			SELECT DISTINCT ON (thread_id)
				thread_id, thread_key_source, trace_id, start_time, end_time,
				status, error, first_user_preview, last_ai_preview, project
			FROM roots
			ORDER BY thread_id, start_time DESC NULLS LAST, trace_id DESC
		)
		SELECT a.thread_id, COALESCE(l.thread_key_source, ''), a.turn_count,
			a.first_activity, a.last_activity, a.error_count,
			l.trace_id, l.start_time, l.end_time, l.status, l.error,
			l.first_user_preview, l.last_ai_preview, COALESCE(l.project, '')
		FROM agg a
		JOIN latest l ON l.thread_id = a.thread_id
		%s
		ORDER BY a.last_activity DESC NULLS LAST, a.thread_id DESC
		LIMIT %d`,
		strings.Join(where, " AND "),
		TraceFailedExistsSQL("roots.trace_id"),
		curClause,
		opts.Limit+1,
	)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	type row struct {
		threadID, keySource, project string
		turnCount, errCount          int
		first, last                  *time.Time
		latestTrace                  uuid.UUID
		latestStart, latestEnd       *time.Time
		latestStatus                 string
		latestErr                    *string
		firstUser, lastAI            string
	}
	var list []row
	for rows.Next() {
		var rr row
		if err := rows.Scan(
			&rr.threadID, &rr.keySource, &rr.turnCount,
			&rr.first, &rr.last, &rr.errCount,
			&rr.latestTrace, &rr.latestStart, &rr.latestEnd, &rr.latestStatus, &rr.latestErr,
			&rr.firstUser, &rr.lastAI, &rr.project,
		); err != nil {
			return nil, "", err
		}
		list = append(list, rr)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var nextCursor string
	if len(list) > opts.Limit {
		last := list[opts.Limit-1]
		if last.last != nil {
			nextCursor = EncodeCursor(*last.last, last.threadID)
		}
		list = list[:opts.Limit]
	}

	traceIDs := make([]uuid.UUID, 0, len(list))
	for _, rr := range list {
		traceIDs = append(traceIDs, rr.latestTrace)
	}
	failed, oldestOpen, err := s.threadLatestFlags(ctx, traceIDs)
	if err != nil {
		return nil, "", err
	}

	out := make([]map[string]any, 0, len(list))
	now := time.Now().UTC()
	for _, rr := range list {
		status := latestTurnStatus(failed[rr.latestTrace], oldestOpen[rr.latestTrace], now)
		item := map[string]any{
			"thread_id":         rr.threadID,
			"thread_key_source": rr.keySource,
			"turn_count":        rr.turnCount,
			"error_count":       rr.errCount,
			"status":            status,
			"project":           rr.project,
		}
		if rr.first != nil {
			item["first_activity"] = rr.first.UTC().Format(time.RFC3339Nano)
		} else {
			item["first_activity"] = nil
		}
		if rr.last != nil {
			item["last_activity"] = rr.last.UTC().Format(time.RFC3339Nano)
		} else {
			item["last_activity"] = nil
		}
		if rr.firstUser != "" {
			item["first_user_preview"] = rr.firstUser
		} else {
			item["first_user_preview"] = nil
		}
		if rr.lastAI != "" {
			item["last_ai_preview"] = rr.lastAI
		} else {
			item["last_ai_preview"] = nil
		}
		out = append(out, item)
	}
	return out, nextCursor, nil
}

func latestTurnStatus(failed bool, oldestOpen *time.Time, now time.Time) string {
	if failed {
		return "error"
	}
	if oldestOpen != nil {
		if now.Sub(oldestOpen.UTC()) >= traces.StaleAfter() {
			return "stale"
		}
		return "running"
	}
	return "success"
}

func (s *Store) threadLatestFlags(ctx context.Context, traceIDs []uuid.UUID) (failed map[uuid.UUID]bool, oldestOpen map[uuid.UUID]*time.Time, err error) {
	failed = map[uuid.UUID]bool{}
	oldestOpen = map[uuid.UUID]*time.Time{}
	if len(traceIDs) == 0 {
		return failed, oldestOpen, nil
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT t.tid,
			%s AS failed,
			(SELECT MIN(o.start_time) FROM runs o WHERE o.trace_id = t.tid AND o.end_time IS NULL) AS oldest_open
		FROM UNNEST($1::uuid[]) AS t(tid)`, TraceFailedExistsSQL("t.tid")), traceIDs)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tid uuid.UUID
		var isFailed bool
		var open *time.Time
		if err := rows.Scan(&tid, &isFailed, &open); err != nil {
			return nil, nil, err
		}
		failed[tid] = isFailed
		if open != nil {
			oldestOpen[tid] = open
		}
	}
	return failed, oldestOpen, rows.Err()
}

// UIListThreadTraces returns TraceSummary rows for one thread, oldest turn first.
func (s *Store) UIListThreadTraces(ctx context.Context, project, threadID string, limit int, cursorStart *time.Time, cursorTraceID string) ([]map[string]any, string, error) {
	if threadID == "" {
		return nil, "", fmt.Errorf("thread_id required")
	}
	return s.UIListTraces(ctx, TraceListOpts{
		Project:       project,
		ThreadID:      threadID,
		Limit:         limit,
		Sort:          "start_asc",
		CursorStart:   cursorStart,
		CursorTraceID: cursorTraceID,
	})
}
