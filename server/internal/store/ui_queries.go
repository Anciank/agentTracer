package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Anciank/agentTracer/server/internal/traces"
)

type TraceListOpts struct {
	Project       string
	Q             string
	Status        string
	RunType       string
	Model         string
	MinTokens     int
	MinDurationMs int
	From, To      *time.Time
	Sort          string
	Limit         int
	CursorStart   *time.Time
	CursorTraceID string
	// M3.5 Agent-map filters (compose with existing; apply after root fetch).
	Node     string // node_key exact
	NodeFail string // node_key that must have an error visit
	Edge     string // "from>to"
	Path     string // path_fingerprint exact
	Tool     string // M3.6: exact match on a run_type=tool name in the trace
}

type cursorPayload struct {
	S string `json:"s"`
	T string `json:"t"`
}

func EncodeCursor(start time.Time, traceID string) string {
	b, _ := json.Marshal(cursorPayload{S: start.UTC().Format(time.RFC3339Nano), T: traceID})
	return base64.RawURLEncoding.EncodeToString(b)
}

func DecodeCursor(s string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", err
	}
	var p cursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return time.Time{}, "", err
	}
	t := parseTime(p.S)
	if t == nil {
		return time.Time{}, "", fmt.Errorf("bad cursor time")
	}
	return *t, p.T, nil
}

func (s *Store) UIProjects(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name FROM sessions ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type sess struct {
		id   uuid.UUID
		name string
	}
	var sessions []sess
	for rows.Next() {
		var x sess
		if err := rows.Scan(&x.id, &x.name); err != nil {
			return nil, err
		}
		sessions = append(sessions, x)
	}
	out := make([]map[string]any, 0, len(sessions))
	now := time.Now().UTC()
	dayAgo := now.Add(-24 * time.Hour)
	weekAgo := now.Add(-7 * 24 * time.Hour)
	for _, sess := range sessions {
		card := map[string]any{
			"id": sess.id.String(), "name": sess.name,
			"trace_count_24h": 0, "trace_count_7d": 0,
			"error_rate_24h": 0.0, "p50_ms": nil, "p95_ms": nil, "tokens_24h": 0,
		}
		// root runs in windows
		type rootRow struct {
			traceID uuid.UUID
			start   time.Time
			end     *time.Time
			failed  bool
			tokens  int
		}
		failedPred := TraceFailedExistsSQL("runs.trace_id")
		qrows, err := s.pool.Query(ctx, `
			SELECT trace_id, start_time, end_time, (`+failedPred+`) AS is_failed, total_tokens
			FROM runs
			WHERE session_id=$1 AND parent_run_id IS NULL AND start_time IS NOT NULL AND start_time >= $2`,
			sess.id, weekAgo)
		if err != nil {
			return nil, err
		}
		var roots []rootRow
		for qrows.Next() {
			var r rootRow
			if err := qrows.Scan(&r.traceID, &r.start, &r.end, &r.failed, &r.tokens); err != nil {
				qrows.Close()
				return nil, err
			}
			roots = append(roots, r)
		}
		qrows.Close()
		var latencies []float64
		err24, total24, tok24 := 0, 0, 0
		count7 := 0
		for _, r := range roots {
			count7++
			if r.start.Before(dayAgo) {
				continue
			}
			total24++
			tok24 += r.tokens
			if r.failed {
				err24++
			}
			if r.end != nil {
				latencies = append(latencies, float64(r.end.Sub(r.start).Milliseconds()))
			}
		}
		// tokens_24h: sum all runs in session in 24h (prefer richer than root-only)
		var sumTok int
		_ = s.pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(total_tokens),0) FROM runs
			WHERE session_id=$1 AND start_time >= $2`, sess.id, dayAgo).Scan(&sumTok)
		if sumTok > 0 {
			tok24 = sumTok
		}
		card["trace_count_24h"] = total24
		card["trace_count_7d"] = count7
		card["tokens_24h"] = tok24
		if total24 > 0 {
			card["error_rate_24h"] = float64(err24) / float64(total24)
		}
		if len(latencies) > 0 {
			sort.Float64s(latencies)
			card["p50_ms"] = percentile(latencies, 0.50)
			card["p95_ms"] = percentile(latencies, 0.95)
		}
		out = append(out, card)
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out, nil
}

func percentile(sorted []float64, p float64) int {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return int(math.Round(sorted[0]))
	}
	idx := p * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return int(math.Round(sorted[lo]))
	}
	w := idx - float64(lo)
	return int(math.Round(sorted[lo]*(1-w) + sorted[hi]*w))
}

func (s *Store) UIListTraces(ctx context.Context, opts TraceListOpts) ([]map[string]any, string, error) {
	if opts.Limit <= 0 || opts.Limit > 200 {
		opts.Limit = 50
	}
	args := []any{}
	n := 1
	where := []string{"r.parent_run_id IS NULL", "r.trace_id IS NOT NULL"}
	if opts.Project != "" {
		where = append(where, fmt.Sprintf("s.name=$%d", n))
		args = append(args, opts.Project)
		n++
	}
	if opts.From != nil {
		where = append(where, fmt.Sprintf("r.start_time >= $%d", n))
		args = append(args, *opts.From)
		n++
	}
	if opts.To != nil {
		where = append(where, fmt.Sprintf("r.start_time < $%d", n))
		args = append(args, *opts.To)
		n++
	}
	if opts.Model != "" {
		where = append(where, fmt.Sprintf(
			`EXISTS (SELECT 1 FROM runs x WHERE x.trace_id=r.trace_id AND x.model=$%d)`, n))
		args = append(args, opts.Model)
		n++
	}
	if opts.Tool != "" {
		where = append(where, fmt.Sprintf(
			`EXISTS (SELECT 1 FROM runs x WHERE x.trace_id=r.trace_id AND x.run_type='tool' AND x.name=$%d)`, n))
		args = append(args, opts.Tool)
		n++
	}
	if opts.RunType != "" {
		where = append(where, fmt.Sprintf(
			`EXISTS (SELECT 1 FROM runs x WHERE x.trace_id=r.trace_id AND x.run_type=$%d)`, n))
		args = append(args, opts.RunType)
		n++
	}
	if opts.Q != "" {
		where = append(where, fmt.Sprintf(
			`(r.name ILIKE $%d OR EXISTS (SELECT 1 FROM runs x WHERE x.trace_id=r.trace_id AND x.error ILIKE $%d))`, n, n))
		args = append(args, "%"+opts.Q+"%")
		n++
	}
	switch opts.Status {
	case "error":
		where = append(where, TraceFailedExistsSQL("r.trace_id"))
	case "success":
		// completed root, no failed run in the trace
		where = append(where, "r.end_time IS NOT NULL")
		where = append(where, "NOT "+TraceFailedExistsSQL("r.trace_id"))
	case "running":
		where = append(where, "r.end_time IS NULL")
		where = append(where, "NOT "+TraceFailedExistsSQL("r.trace_id"))
	}
	if opts.CursorStart != nil && opts.CursorTraceID != "" {
		where = append(where, fmt.Sprintf(
			`(r.start_time, r.trace_id) < ($%d::timestamptz, $%d::uuid)`, n, n+1))
		args = append(args, *opts.CursorStart, opts.CursorTraceID)
		n += 2
	}
	if opts.Path != "" {
		where = append(where, fmt.Sprintf(`r.path_fingerprint=$%d`, n))
		args = append(args, opts.Path)
		n++
	}

	order := "r.start_time DESC NULLS LAST, r.trace_id DESC"
	switch opts.Sort {
	case "duration_desc":
		order = "(EXTRACT(EPOCH FROM (r.end_time-r.start_time))) DESC NULLS LAST, r.trace_id DESC"
	case "tokens_desc":
		order = "r.total_tokens DESC, r.trace_id DESC"
	case "errors_desc":
		order = `(` + TraceFailedExistsSQL("r.trace_id") + `) DESC, r.start_time DESC, r.trace_id DESC`
	}

	q := fmt.Sprintf(`
		SELECT r.trace_id, r.name, s.name, r.start_time, r.end_time, r.status, r.error,
			r.total_tokens, r.input_tokens, r.output_tokens, r.model, r.session_id,
			r.first_user_preview, r.last_ai_preview
		FROM runs r
		LEFT JOIN sessions s ON s.id = r.session_id
		WHERE %s
		ORDER BY %s
		LIMIT %d`, strings.Join(where, " AND "), order, opts.Limit+1)
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	type root struct {
		traceID             uuid.UUID
		name, status, model string
		project             *string
		start               *time.Time
		end                 *time.Time
		err                 *string
		tok, inTok, outTok  int
		sessionID           *uuid.UUID
		firstUser, lastAI   string
	}
	var roots []root
	for rows.Next() {
		var rr root
		if err := rows.Scan(&rr.traceID, &rr.name, &rr.project, &rr.start, &rr.end, &rr.status, &rr.err,
			&rr.tok, &rr.inTok, &rr.outTok, &rr.model, &rr.sessionID, &rr.firstUser, &rr.lastAI); err != nil {
			return nil, "", err
		}
		roots = append(roots, rr)
	}

	var nextCursor string
	if len(roots) > opts.Limit {
		last := roots[opts.Limit-1]
		if last.start != nil {
			nextCursor = EncodeCursor(*last.start, last.traceID.String())
		}
		roots = roots[:opts.Limit]
	}

	out := make([]map[string]any, 0, len(roots))
	for _, rr := range roots {
		runs, err := s.QueryRuns(ctx, QueryOpts{Trace: rr.traceID.String(), Unlimited: true})
		if err != nil {
			return nil, "", err
		}
		project := ""
		if rr.project != nil {
			project = *rr.project
		}
		sum := buildUITraceSummary(rr.traceID.String(), project, runs)
		fu, la := rr.firstUser, rr.lastAI
		if fu == "" || la == "" {
			f2, l2 := pickTraceChatPreviews(runs)
			if fu == "" {
				fu = f2
			}
			if la == "" {
				la = l2
			}
		}
		if fu != "" {
			sum["first_user_preview"] = fu
		} else {
			sum["first_user_preview"] = nil
		}
		if la != "" {
			sum["last_ai_preview"] = la
		} else {
			sum["last_ai_preview"] = nil
		}
		if opts.Status != "" && asString(sum["status"]) != opts.Status {
			continue
		}
		if opts.MinTokens > 0 {
			toks, _ := sum["tokens"].(map[string]any)
			if asInt(toks["total"]) < opts.MinTokens {
				continue
			}
		}
		if opts.MinDurationMs > 0 {
			if dm, ok := sum["duration_ms"].(int); !ok || dm < opts.MinDurationMs {
				continue
			}
		}
		if opts.Node != "" || opts.NodeFail != "" || opts.Edge != "" {
			visits, err := s.loadTraceVisits(ctx, rr.traceID)
			if err != nil {
				return nil, "", err
			}
			if opts.Node != "" && !TraceVisitsMatch(visits, opts.Node, false) {
				continue
			}
			if opts.NodeFail != "" && !TraceVisitsMatch(visits, opts.NodeFail, true) {
				continue
			}
			if opts.Edge != "" {
				parts := strings.SplitN(opts.Edge, ">", 2)
				if len(parts) != 2 || !TraceHasEdge(visits, parts[0], parts[1]) {
					continue
				}
			}
		}
		out = append(out, sum)
	}
	return out, nextCursor, nil
}

func buildUITraceSummary(traceID, project string, runs []map[string]any) map[string]any {
	errCount, running, stale := 0, 0, 0
	inTok, outTok, totTok := 0, 0, 0
	types := map[string]int{}
	modelCounts := map[string]int{}
	var startMin, endMax *time.Time
	name := "(unnamed)"
	for _, r := range runs {
		if r["parent_run_id"] == nil || r["parent_run_id"] == "" {
			if n := asString(r["name"]); n != "" {
				name = n
			}
		}
		st := runStatus(r)
		switch st {
		case "error":
			errCount++
		case "running":
			running++
		case "stale":
			stale++
		}
		rt := asString(r["run_type"])
		if rt == "" {
			rt = "unknown"
		}
		types[rt]++
		inTok += asInt(r["input_tokens"])
		outTok += asInt(r["output_tokens"])
		totTok += asInt(r["total_tokens"])
		if m := asString(r["model"]); m != "" {
			modelCounts[m]++
		}
		if t := parseTime(r["start_time"]); t != nil {
			if startMin == nil || t.Before(*startMin) {
				startMin = t
			}
		}
		if t := parseTime(r["end_time"]); t != nil {
			if endMax == nil || t.After(*endMax) {
				endMax = t
			}
		}
	}
	status := "success"
	if errCount > 0 {
		status = "error"
	} else if running > 0 {
		status = "running"
	} else if stale > 0 {
		status = "stale"
	}
	var model any
	best, bestN := "", 0
	for m, c := range modelCounts {
		if c > bestN || (c == bestN && m < best) {
			best, bestN = m, c
		}
	}
	if best != "" {
		model = best
	}
	out := map[string]any{
		"trace_id": traceID, "name": name, "project": project,
		"run_count": len(runs), "error_count": errCount, "running_count": running, "stale_count": stale,
		"status": status,
		"tokens": map[string]any{"input": inTok, "output": outTok, "total": totTok},
		"model":  model, "run_types": types,
	}
	if startMin != nil {
		out["start"] = startMin.UTC().Format(time.RFC3339Nano)
	} else {
		out["start"] = nil
	}
	if endMax != nil && running == 0 && stale == 0 {
		out["end"] = endMax.UTC().Format(time.RFC3339Nano)
	} else {
		out["end"] = nil
	}
	if startMin != nil && endMax != nil && running == 0 && stale == 0 {
		out["duration_ms"] = int(endMax.Sub(*startMin).Milliseconds())
	} else {
		out["duration_ms"] = nil
	}
	return out
}

func runStatus(r map[string]any) string {
	return traces.RunStatus(r)
}

func (s *Store) UIStats(ctx context.Context, project string, from, to *time.Time) (map[string]any, error) {
	now := time.Now().UTC()
	if to == nil {
		t := now
		to = &t
	}
	if from == nil {
		f := to.Add(-24 * time.Hour)
		from = &f
	}
	args := []any{*from, *to}
	sessFilter := ""
	if project != "" {
		sessFilter = " AND s.name=$3"
		args = append(args, project)
	}
	// roots for latency / error / count — error uses shared any-run failed definition
	failedPred := TraceFailedExistsSQL("r.trace_id")
	q := `
		SELECT r.trace_id, r.start_time, r.end_time, (` + failedPred + `) AS is_failed
		FROM runs r LEFT JOIN sessions s ON s.id=r.session_id
		WHERE r.parent_run_id IS NULL AND r.start_time >= $1 AND r.start_time < $2` + sessFilter
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var lats []float64
	traces, errors := 0, 0
	for rows.Next() {
		var tid uuid.UUID
		var start time.Time
		var end *time.Time
		var isFailed bool
		if err := rows.Scan(&tid, &start, &end, &isFailed); err != nil {
			return nil, err
		}
		traces++
		if isFailed {
			errors++
		}
		if end != nil {
			lats = append(lats, float64(end.Sub(start).Milliseconds()))
		}
	}
	var inSum, outSum, totSum int
	tokQ := `
		SELECT COALESCE(SUM(r.input_tokens),0), COALESCE(SUM(r.output_tokens),0), COALESCE(SUM(r.total_tokens),0)
		FROM runs r LEFT JOIN sessions s ON s.id=r.session_id
		WHERE r.start_time >= $1 AND r.start_time < $2` + sessFilter
	_ = s.pool.QueryRow(ctx, tokQ, args...).Scan(&inSum, &outSum, &totSum)

	byDay := []map[string]any{}
	dayQ := `
		SELECT date_trunc('day', r.start_time AT TIME ZONE 'UTC')::date AS day,
			COUNT(DISTINCT r.trace_id) FILTER (WHERE r.parent_run_id IS NULL),
			COUNT(DISTINCT r.trace_id) FILTER (WHERE r.parent_run_id IS NULL AND ` + failedPred + `),
			COALESCE(SUM(r.total_tokens),0)
		FROM runs r LEFT JOIN sessions s ON s.id=r.session_id
		WHERE r.start_time >= $1 AND r.start_time < $2` + sessFilter + `
		GROUP BY 1 ORDER BY 1`
	drows, err := s.pool.Query(ctx, dayQ, args...)
	if err != nil {
		return nil, err
	}
	for drows.Next() {
		var day time.Time
		var tc, ec, tok int
		if err := drows.Scan(&day, &tc, &ec, &tok); err != nil {
			drows.Close()
			return nil, err
		}
		byDay = append(byDay, map[string]any{
			"day": day.Format("2006-01-02"), "traces": tc, "errors": ec, "tokens": tok,
		})
	}
	drows.Close()

	byModel := []map[string]any{}
	mQ := `
		SELECT COALESCE(NULLIF(r.model,''),'(unknown)'), COUNT(*), COALESCE(SUM(r.total_tokens),0)
		FROM runs r LEFT JOIN sessions s ON s.id=r.session_id
		WHERE r.start_time >= $1 AND r.start_time < $2 AND r.run_type='llm'` + sessFilter + `
		GROUP BY 1 ORDER BY 3 DESC`
	mrows, err := s.pool.Query(ctx, mQ, args...)
	if err != nil {
		return nil, err
	}
	for mrows.Next() {
		var model string
		var calls, tok int
		if err := mrows.Scan(&model, &calls, &tok); err != nil {
			mrows.Close()
			return nil, err
		}
		byModel = append(byModel, map[string]any{"model": model, "calls": calls, "tokens": tok})
	}
	mrows.Close()

	out := map[string]any{
		"trace_count": traces,
		"error_rate":  0.0,
		"p50_ms":      nil,
		"p95_ms":      nil,
		"tokens":      map[string]any{"input": inSum, "output": outSum, "total": totSum},
		"by_day":      byDay,
		"by_model":    byModel,
	}
	if traces > 0 {
		out["error_rate"] = float64(errors) / float64(traces)
	}
	if len(lats) > 0 {
		sort.Float64s(lats)
		out["p50_ms"] = percentile(lats, 0.50)
		out["p95_ms"] = percentile(lats, 0.95)
	}
	return out, nil
}
