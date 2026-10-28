package api

import (
	"context"
	"errors"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/Anciank/agentTracer/server/internal/live"
	"github.com/Anciank/agentTracer/server/internal/store"
	"github.com/Anciank/agentTracer/server/internal/traces"
)

const truncateBytes = 2 << 20 // 2 MiB
const previewBytes = 64 << 10

func (s *Server) routesUI() {
	s.mux.HandleFunc("GET /api/ui/projects", s.apiUIProjects)
	s.mux.HandleFunc("GET /api/ui/traces", s.apiUITraces)
	s.mux.HandleFunc("GET /api/ui/traces/stream", s.apiUITracesStream)
	s.mux.HandleFunc("GET /api/ui/traces/{id}/summary", s.apiUITraceSummary)
	s.mux.HandleFunc("GET /api/ui/runs/{id}", s.apiUIRun)
	s.mux.HandleFunc("GET /api/ui/runs/{id}/messages", s.apiUIRunMessages)
	s.mux.HandleFunc("GET /api/ui/runs/{id}/attachments/{name}", s.apiUIAttachment)
	s.mux.HandleFunc("GET /api/ui/stats", s.apiUIStats)
	s.mux.HandleFunc("GET /api/ui/projects/{id}/graph", s.apiUIGraph)
	s.mux.HandleFunc("GET /api/ui/projects/{id}/paths", s.apiUIPaths)
}

func (s *Server) apiUIProjects(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.UIProjects(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 200, list)
}

func parseUITraceOpts(r *http.Request) (store.TraceListOpts, error) {
	q := r.URL.Query()
	opts := store.TraceListOpts{
		Project: q.Get("project"),
		Q:       q.Get("q"),
		Status:  q.Get("status"),
		RunType: q.Get("run_type"),
		Model:   q.Get("model"),
		Sort:    q.Get("sort"),
	}
	if v := q.Get("min_tokens"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return opts, fmt.Errorf("min_tokens: %w", err)
		}
		opts.MinTokens = n
	}
	if v := q.Get("min_duration_ms"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return opts, fmt.Errorf("min_duration_ms: %w", err)
		}
		opts.MinDurationMs = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return opts, fmt.Errorf("limit must be 1..200")
		}
		opts.Limit = n
	}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			t, err = time.Parse(time.RFC3339, v)
		}
		if err != nil {
			return opts, fmt.Errorf("from: %w", err)
		}
		opts.From = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			t, err = time.Parse(time.RFC3339, v)
		}
		if err != nil {
			return opts, fmt.Errorf("to: %w", err)
		}
		opts.To = &t
	}
	if c := q.Get("cursor"); c != "" {
		st, tid, err := store.DecodeCursor(c)
		if err != nil {
			return opts, fmt.Errorf("invalid cursor")
		}
		opts.CursorStart = &st
		opts.CursorTraceID = tid
	}
	opts.Node = q.Get("node")
	opts.NodeFail = q.Get("node_fail")
	opts.Edge = q.Get("edge")
	opts.Path = q.Get("path")
	if opts.Edge != "" {
		parts := splitEdge(opts.Edge)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return opts, fmt.Errorf("edge must be from>to")
		}
	}
	return opts, nil
}

func splitEdge(s string) []string {
	for i := 0; i < len(s); i++ {
		if s[i] == '>' {
			return []string{s[:i], s[i+1:]}
		}
	}
	return nil
}

func (s *Server) apiUITraces(w http.ResponseWriter, r *http.Request) {
	opts, err := parseUITraceOpts(r)
	if err != nil {
		writeJSON(w, 422, map[string]any{"detail": err.Error()})
		return
	}
	list, next, err := s.store.UIListTraces(r.Context(), opts)
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	var nextAny any
	if next != "" {
		nextAny = next
	}
	writeJSON(w, 200, map[string]any{"traces": list, "next_cursor": nextAny})
}

func (s *Server) apiUITraceSummary(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	runs, err := s.store.QueryRuns(r.Context(), store.QueryOpts{Trace: id, Unlimited: true})
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	if len(runs) == 0 {
		writeJSON(w, 404, map[string]any{"detail": "trace not found"})
		return
	}
	project := ""
	for _, run := range runs {
		if sid, ok := run["session_id"].(string); ok && sid != "" {
			if sess, err := s.store.GetSession(r.Context(), sid); err == nil {
				project = asStr(sess["name"])
			}
			break
		}
	}
	summary := buildUITraceSummaryFromRuns(id, project, runs)
	rows := buildSpanSummaries(runs)
	var spanMs any
	if dm, ok := summary["duration_ms"].(int); ok {
		spanMs = dm
	}
	writeJSON(w, 200, map[string]any{
		"trace_id": id, "span_ms": spanMs, "summary": summary, "rows": rows,
	})
}

func buildUITraceSummaryFromRuns(traceID, project string, runs []map[string]any) map[string]any {
	// reuse store helper logic via local copy of fields
	errCount, running := 0, 0
	inTok, outTok, totTok := 0, 0, 0
	types := map[string]int{}
	modelCounts := map[string]int{}
	var startMin, endMax *time.Time
	name := "(unnamed)"
	for _, r := range runs {
		if r["parent_run_id"] == nil || r["parent_run_id"] == "" {
			if n := asStr(r["name"]); n != "" {
				name = n
			}
		}
		st := traces.RunStatus(r)
		if st == "error" {
			errCount++
		}
		if st == "running" {
			running++
		}
		rt := asStr(r["run_type"])
		if rt == "" {
			rt = "unknown"
		}
		types[rt]++
		inTok += asIntAny(r["input_tokens"])
		outTok += asIntAny(r["output_tokens"])
		totTok += asIntAny(r["total_tokens"])
		if m := asStr(r["model"]); m != "" {
			modelCounts[m]++
		}
		if t := parseTimeAny(r["start_time"]); t != nil {
			if startMin == nil || t.Before(*startMin) {
				startMin = t
			}
		}
		if t := parseTimeAny(r["end_time"]); t != nil {
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
	}
	var model any
	best, bestN := "", 0
	for m, c := range modelCounts {
		if c > bestN || (c == bestN && (best == "" || m < best)) {
			best, bestN = m, c
		}
	}
	if best != "" {
		model = best
	}
	out := map[string]any{
		"trace_id": traceID, "name": name, "project": project,
		"run_count": len(runs), "error_count": errCount, "running_count": running,
		"status": status,
		"tokens": map[string]any{"input": inTok, "output": outTok, "total": totTok},
		"model":  model, "run_types": types,
	}
	if startMin != nil {
		out["start"] = startMin.UTC().Format(time.RFC3339Nano)
	} else {
		out["start"] = nil
	}
	if endMax != nil && running == 0 {
		out["end"] = endMax.UTC().Format(time.RFC3339Nano)
	} else {
		out["end"] = nil
	}
	if startMin != nil && endMax != nil && running == 0 {
		out["duration_ms"] = int(endMax.Sub(*startMin).Milliseconds())
	} else {
		out["duration_ms"] = nil
	}
	return out
}

func buildSpanSummaries(runs []map[string]any) []map[string]any {
	if len(runs) == 0 {
		return []map[string]any{}
	}
	byID := map[string]map[string]any{}
	for _, r := range runs {
		byID[asStr(r["id"])] = r
	}
	var t0 *time.Time
	for _, r := range runs {
		if st := parseTimeAny(r["start_time"]); st != nil {
			if t0 == nil || st.Before(*t0) {
				t0 = st
			}
		}
	}
	out := make([]map[string]any, 0, len(runs))
	// sort via traces.TraceDetail order by reusing dotted_order
	detail := traces.TraceDetail(asStr(runs[0]["trace_id"]), runs)
	ordered := runs
	if detail != nil {
		if rows, ok := detail["rows"].([]map[string]any); ok {
			// rebuild from original by id order in detail rows
			ordered = make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				if orig, ok := byID[asStr(row["id"])]; ok {
					ordered = append(ordered, orig)
				}
			}
		}
	}
	attsCount := map[string]int{}
	for _, r := range runs {
		_ = r
	}
	for _, run := range ordered {
		start := parseTimeAny(run["start_time"])
		end := parseTimeAny(run["end_time"])
		var lat, self any
		if start != nil && end != nil {
			lat = int(end.Sub(*start).Milliseconds())
		}
		if st := traces.SelfTimeMs(run, runs); st != nil {
			self = *st
		}
		offset := 0
		if start != nil && t0 != nil {
			offset = int(start.Sub(*t0).Milliseconds())
		}
		tags := run["tags"]
		if tags == nil {
			tags = []any{}
		}
		depth := traces.DepthOf(run, byID)
		payloadHot := true
		for _, k := range []string{"inputs", "outputs", "extra", "events", "serialized"} {
			if isColdPointer(run[k]) {
				payloadHot = false
				break
			}
		}
		out = append(out, map[string]any{
			"id": run["id"], "parent_run_id": run["parent_run_id"],
			"name": run["name"], "run_type": orDef(asStr(run["run_type"]), "chain"),
			"node": traces.RunNode(run), "status": traces.RunStatus(run),
			"depth": depth, "offset_ms": offset, "latency_ms": lat, "self_time_ms": self,
			"start_time": run["start_time"], "end_time": run["end_time"],
			"tokens": map[string]any{
				"input": asIntAny(run["input_tokens"]), "output": asIntAny(run["output_tokens"]),
				"total": asIntAny(run["total_tokens"]),
			},
			"model": run["model"], "tags": tags, "dotted_order": run["dotted_order"],
			"has_error": asStr(run["error"]) != "", "payload_hot": payloadHot,
			"attachment_count": attsCount[asStr(run["id"])],
		})
	}
	return out
}

func (s *Server) apiUIRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	full := r.URL.Query().Get("full") == "1"
	run, err := s.store.GetRun(r.Context(), id)
	if err != nil {
		writeJSON(w, 404, map[string]any{"detail": "not found"})
		return
	}
	atts, _ := s.store.ListAttachments(r.Context(), id)
	attOut := make([]map[string]any, 0, len(atts))
	for _, a := range atts {
		attOut = append(attOut, map[string]any{
			"name": a["name"], "content_type": a["content_type"], "size": a["size"],
			"url": fmt.Sprintf("/api/ui/runs/%s/attachments/%s", id, a["name"]),
		})
	}
	runs, _ := s.store.QueryRuns(r.Context(), store.QueryOpts{Trace: asStr(run["trace_id"]), Unlimited: true})
	self := traces.SelfTimeMs(run, runs)
	var selfAny any
	if self != nil {
		selfAny = *self
	}
	var lat any
	if st := parseTimeAny(run["start_time"]); st != nil {
		if et := parseTimeAny(run["end_time"]); et != nil {
			lat = int(et.Sub(*st).Milliseconds())
		}
	}
	truncated := false
	cold := false
	maybe := func(v any) any {
		if isColdPointer(v) {
			cold = true
		}
		if full {
			return v
		}
		tv, was := maybeTruncate(v)
		if was {
			truncated = true
		}
		return tv
	}
	tags := run["tags"]
	if tags == nil {
		tags = []any{}
	}
	out := map[string]any{
		"id": run["id"], "parent_run_id": run["parent_run_id"], "trace_id": run["trace_id"],
		"session_id": run["session_id"], "name": run["name"], "run_type": run["run_type"],
		"node": traces.RunNode(run), "status": traces.RunStatus(run),
		"start_time": run["start_time"], "end_time": run["end_time"],
		"latency_ms": lat, "self_time_ms": selfAny,
		"tokens": map[string]any{
			"input": asIntAny(run["input_tokens"]), "output": asIntAny(run["output_tokens"]),
			"total": asIntAny(run["total_tokens"]),
		},
		"model": run["model"], "tags": tags, "dotted_order": run["dotted_order"],
		"path_fingerprint": run["path_fingerprint"],
		"inputs":           maybe(run["inputs"]), "outputs": maybe(run["outputs"]),
		"extra": maybe(run["extra"]), "events": maybe(run["events"]),
		"serialized": maybe(run["serialized"]), "error": run["error"],
		"attachments": attOut, "cold": cold, "truncated": truncated,
	}
	writeJSON(w, 200, out)
}

func (s *Server) apiUIRunMessages(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := s.store.GetRun(r.Context(), id)
	if err != nil {
		writeJSON(w, 404, map[string]any{"detail": "not found"})
		return
	}
	writeJSON(w, 200, NormalizeMessages(run["inputs"], run["outputs"]))
}

func (s *Server) apiUIAttachment(w http.ResponseWriter, r *http.Request) {
	data, ct, err := s.store.GetAttachment(r.Context(), r.PathValue("id"), r.PathValue("name"))
	if err != nil {
		writeJSON(w, 404, map[string]any{"detail": "not found"})
		return
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(200)
	_, _ = w.Write(data)
}

func (s *Server) apiUIStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var from, to *time.Time
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			t, err = time.Parse(time.RFC3339, v)
		}
		if err != nil {
			writeJSON(w, 422, map[string]any{"detail": "from: " + err.Error()})
			return
		}
		from = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			t, err = time.Parse(time.RFC3339, v)
		}
		if err != nil {
			writeJSON(w, 422, map[string]any{"detail": "to: " + err.Error()})
			return
		}
		to = &t
	}
	stats, err := s.store.UIStats(r.Context(), q.Get("project"), from, to)
	if err != nil {
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 200, stats)
}

func (s *Server) apiUITracesStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, 500, map[string]any{"detail": "streaming unsupported"})
		return
	}
	opts, err := parseUITraceOpts(r)
	if err != nil {
		writeJSON(w, 422, map[string]any{"detail": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(200)
	flusher.Flush()

	seenTrace := map[string]bool{}
	if last := r.Header.Get("Last-Event-ID"); last != "" {
		if st, tid, err := store.DecodeCursor(last); err == nil {
			opts.CursorStart = &st
			opts.CursorTraceID = tid
		}
	}

	hub := s.store.Hub()
	var events <-chan live.RunEvent
	var unsub func()
	if hub != nil {
		events, unsub = hub.Subscribe(256)
		defer unsub()
	} else {
		ch := make(chan live.RunEvent)
		events = ch
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	writeEvent := func(id, name string, payload any) {
		b, _ := json.Marshal(payload)
		if id != "" {
			_, _ = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", id, name, b)
		} else {
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
		}
		flusher.Flush()
	}

	matchFilters := func(project string, run map[string]any) bool {
		if opts.Project != "" && project != "" && project != opts.Project {
			return false
		}
		if opts.RunType != "" && asStr(run["run_type"]) != opts.RunType {
			// still emit; filter is "trace contains type" — skip only if we know mismatch for this run alone is wrong
			// Keep emitting; client filters waterfall.
		}
		if opts.Model != "" && asStr(run["model"]) != "" && asStr(run["model"]) != opts.Model {
			// same — emit for live waterfall
		}
		return true
	}

	emitRunUpsert := func(ev live.RunEvent) {
		if !matchFilters(ev.Project, ev.Run) {
			return
		}
		row := s.spanSummaryForLive(r.Context(), ev)
		cur := ""
		if st := parseTimeAny(ev.Run["start_time"]); st != nil {
			cur = store.EncodeCursor(*st, ev.TraceID)
		}
		writeEvent(cur, "run_upsert", map[string]any{"trace_id": ev.TraceID, "row": row})

		// Also emit/refresh TraceSummary for list when root changes or first sight.
		isRoot := ev.Run["parent_run_id"] == nil || asStr(ev.Run["parent_run_id"]) == ""
		if isRoot || !seenTrace[ev.TraceID] {
			runs, err := s.store.QueryRuns(r.Context(), store.QueryOpts{Trace: ev.TraceID, Unlimited: true})
			if err == nil && len(runs) > 0 {
				sum := buildUITraceSummaryFromRuns(ev.TraceID, ev.Project, runs)
				if opts.Project == "" || asStr(sum["project"]) == opts.Project || opts.Project == ev.Project {
					seenTrace[ev.TraceID] = true
					writeEvent(cur, "trace", sum)
				}
			}
		}
	}

	// Seed recent traces once
	seedOpts := opts
	seedOpts.Limit = 20
	if list, _, err := s.store.UIListTraces(r.Context(), seedOpts); err == nil {
		for i := len(list) - 1; i >= 0; i-- {
			tr := list[i]
			tid := asStr(tr["trace_id"])
			seenTrace[tid] = true
			cur := ""
			if st := asStr(tr["start"]); st != "" {
				if tp := mustParse(st); tp != nil {
					cur = store.EncodeCursor(*tp, tid)
				}
			}
			writeEvent(cur, "trace", tr)
		}
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			writeEvent("", "heartbeat", map[string]any{})
		case ev, ok := <-events:
			if !ok {
				return
			}
			emitRunUpsert(ev)
		}
	}
}

// spanSummaryForLive builds a SpanSummary for a hub event.
// depth/offset_ms are filled from current sibling runs when cheap; may be stale
// until the trace finishes if sibling fetch fails.
func (s *Server) spanSummaryForLive(ctx context.Context, ev live.RunEvent) map[string]any {
	run := ev.Run
	runs := []map[string]any{run}
	if sibs, err := s.store.QueryRuns(ctx, store.QueryOpts{Trace: ev.TraceID, Unlimited: true}); err == nil && len(sibs) > 0 {
		runs = sibs
	}
	byID := map[string]map[string]any{}
	var t0 *time.Time
	for _, r := range runs {
		byID[asStr(r["id"])] = r
		if st := parseTimeAny(r["start_time"]); st != nil {
			if t0 == nil || st.Before(*t0) {
				t0 = st
			}
		}
	}
	start := parseTimeAny(run["start_time"])
	end := parseTimeAny(run["end_time"])
	var lat, self any
	if start != nil && end != nil {
		lat = int(end.Sub(*start).Milliseconds())
	}
	if st := traces.SelfTimeMs(run, runs); st != nil {
		self = *st
	}
	offset := 0
	if start != nil && t0 != nil {
		offset = int(start.Sub(*t0).Milliseconds())
	}
	tags := run["tags"]
	if tags == nil {
		tags = []any{}
	}
	payloadHot := true
	for _, k := range []string{"inputs", "outputs", "extra", "events", "serialized"} {
		if isColdPointer(run[k]) {
			payloadHot = false
			break
		}
	}
	return map[string]any{
		"id": run["id"], "parent_run_id": run["parent_run_id"],
		"name": run["name"], "run_type": orDef(asStr(run["run_type"]), "chain"),
		"node": traces.RunNode(run), "status": traces.RunStatus(run),
		"depth": traces.DepthOf(run, byID), "offset_ms": offset, "latency_ms": lat, "self_time_ms": self,
		"start_time": run["start_time"], "end_time": run["end_time"],
		"tokens": map[string]any{
			"input": asIntAny(run["input_tokens"]), "output": asIntAny(run["output_tokens"]),
			"total": asIntAny(run["total_tokens"]),
		},
		"model": run["model"], "tags": tags, "dotted_order": run["dotted_order"],
		"has_error": asStr(run["error"]) != "", "payload_hot": payloadHot,
		"attachment_count": 0,
	}
}

func mustParse(s string) *time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339, s)
	}
	if err != nil {
		return nil
	}
	return &t
}

func maybeTruncate(v any) (any, bool) {
	if v == nil {
		return nil, false
	}
	b, err := json.Marshal(v)
	if err != nil || len(b) <= truncateBytes {
		return v, false
	}
	preview := b
	if len(preview) > previewBytes {
		preview = preview[:previewBytes]
		// trim to valid utf8
		for !utf8.Valid(preview) && len(preview) > 0 {
			preview = preview[:len(preview)-1]
		}
	}
	var previewVal any
	if err := json.Unmarshal(preview, &previewVal); err != nil {
		previewVal = string(preview)
	}
	return map[string]any{"_truncated": true, "_bytes": len(b), "preview": previewVal}, true
}

func isColdPointer(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	_, has := m["_s3"]
	return has
}

func asIntAny(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	default:
		return 0
	}
}

func parseTimeAny(v any) *time.Time {
	switch t := v.(type) {
	case time.Time:
		return &t
	case *time.Time:
		return t
	case string:
		if t == "" {
			return nil
		}
		tt, err := time.Parse(time.RFC3339Nano, t)
		if err != nil {
			tt, err = time.Parse(time.RFC3339, t)
		}
		if err != nil {
			return nil
		}
		return &tt
	default:
		return nil
	}
}

func orDef(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func (s *Server) apiUIGraph(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("id")
	if project == "" {
		writeJSON(w, 422, map[string]any{"detail": "project required"})
		return
	}
	q := r.URL.Query()
	opts := store.GraphOpts{Project: project, Window: q.Get("window"), Compare: q.Get("compare") == "1"}
	if v := q.Get("top_n"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			writeJSON(w, 422, map[string]any{"detail": "top_n must be 1..100"})
			return
		}
		opts.TopN = n
	}
	if v := q.Get("min_edge_share"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || f > 1 {
			writeJSON(w, 422, map[string]any{"detail": "min_edge_share must be 0..1"})
			return
		}
		opts.MinEdgeShare = f
	}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			t, err = time.Parse(time.RFC3339, v)
		}
		if err != nil {
			writeJSON(w, 422, map[string]any{"detail": "from: " + err.Error()})
			return
		}
		opts.From = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			t, err = time.Parse(time.RFC3339, v)
		}
		if err != nil {
			writeJSON(w, 422, map[string]any{"detail": "to: " + err.Error()})
			return
		}
		opts.To = &t
	}
	if opts.Window != "" && opts.Window != "1h" && opts.Window != "24h" && opts.Window != "7d" && opts.From == nil {
		writeJSON(w, 422, map[string]any{"detail": "window must be 1h|24h|7d"})
		return
	}
	out, err := s.store.UIGraph(r.Context(), opts)
	if err != nil {
		if errors.Is(err, store.ErrProjectNotFound) {
			writeJSON(w, 404, map[string]any{"detail": "not found"})
			return
		}
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) apiUIPaths(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("id")
	if project == "" {
		writeJSON(w, 422, map[string]any{"detail": "project required"})
		return
	}
	q := r.URL.Query()
	opts := store.PathsOpts{Project: project, Window: q.Get("window")}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			writeJSON(w, 422, map[string]any{"detail": "limit must be 1..100"})
			return
		}
		opts.Limit = n
	}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			t, err = time.Parse(time.RFC3339, v)
		}
		if err != nil {
			writeJSON(w, 422, map[string]any{"detail": "from: " + err.Error()})
			return
		}
		opts.From = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			t, err = time.Parse(time.RFC3339, v)
		}
		if err != nil {
			writeJSON(w, 422, map[string]any{"detail": "to: " + err.Error()})
			return
		}
		opts.To = &t
	}
	if opts.Window != "" && opts.Window != "1h" && opts.Window != "24h" && opts.Window != "7d" && opts.From == nil {
		writeJSON(w, 422, map[string]any{"detail": "window must be 1h|24h|7d"})
		return
	}
	out, err := s.store.UIPaths(r.Context(), opts)
	if err != nil {
		if errors.Is(err, store.ErrProjectNotFound) {
			writeJSON(w, 404, map[string]any{"detail": "not found"})
			return
		}
		writeJSON(w, 500, map[string]any{"detail": err.Error()})
		return
	}
	writeJSON(w, 200, out)
}
