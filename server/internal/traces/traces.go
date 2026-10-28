package traces

import (
	"sort"
	"strings"
	"time"
)

func parseTime(v any) *time.Time {
	if v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "Z", "+00:00")
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}

func RunLatency(run map[string]any) *float64 {
	start, end := parseTime(run["start_time"]), parseTime(run["end_time"])
	if start == nil || end == nil {
		return nil
	}
	sec := end.Sub(*start).Seconds()
	return &sec
}

func RunStatus(run map[string]any) string {
	if err, ok := run["error"]; ok && err != nil && err != "" {
		return "error"
	}
	if run["end_time"] == nil || run["end_time"] == "" {
		return "running"
	}
	return "success"
}

func runModel(run map[string]any) any {
	extra, _ := run["extra"].(map[string]any)
	if extra == nil {
		return nil
	}
	meta, _ := extra["metadata"].(map[string]any)
	if meta != nil {
		if n := meta["ls_model_name"]; n != nil {
			return n
		}
		if n := meta["model_name"]; n != nil {
			return n
		}
	}
	inv, _ := extra["invocation_params"].(map[string]any)
	if inv != nil && inv["model"] != nil {
		return inv["model"]
	}
	return nil
}

func runNode(run map[string]any) any {
	extra, _ := run["extra"].(map[string]any)
	if extra == nil {
		return nil
	}
	meta, _ := extra["metadata"].(map[string]any)
	if meta != nil && meta["langgraph_node"] != nil {
		return meta["langgraph_node"]
	}
	return nil
}

func orderKey(run map[string]any) (int, string) {
	if d, ok := run["dotted_order"].(string); ok && d != "" {
		return 0, d
	}
	if s, ok := run["start_time"].(string); ok {
		return 1, s
	}
	return 1, ""
}

func depthOf(run map[string]any, byID map[string]map[string]any) int {
	if d, ok := run["dotted_order"].(string); ok && d != "" {
		return strings.Count(d, ".")
	}
	depth := 0
	seen := map[string]bool{}
	cursor := run
	for {
		pid, _ := cursor["parent_run_id"].(string)
		if pid == "" || seen[pid] {
			break
		}
		seen[pid] = true
		next, ok := byID[pid]
		if !ok {
			break
		}
		cursor = next
		depth++
	}
	return depth
}

func selfTime(run map[string]any, runs []map[string]any) *float64 {
	lat := RunLatency(run)
	if lat == nil {
		return nil
	}
	id, _ := run["id"].(string)
	childTotal := 0.0
	for _, child := range runs {
		pid, _ := child["parent_run_id"].(string)
		if pid != id {
			continue
		}
		if cl := RunLatency(child); cl != nil {
			childTotal += *cl
		}
	}
	v := *lat - childTotal
	if v < 0 {
		v = 0
	}
	return &v
}

func GroupByTrace(runs []map[string]any) map[string][]map[string]any {
	grouped := map[string][]map[string]any{}
	for _, run := range runs {
		tid, _ := run["trace_id"].(string)
		if tid == "" {
			tid, _ = run["id"].(string)
		}
		grouped[tid] = append(grouped[tid], run)
	}
	for _, list := range grouped {
		sort.SliceStable(list, func(i, j int) bool {
			ai, as := orderKey(list[i])
			bj, bs := orderKey(list[j])
			if ai != bj {
				return ai < bj
			}
			return as < bs
		})
	}
	return grouped
}

func TraceSummary(traceID string, runs []map[string]any) map[string]any {
	var roots []map[string]any
	var starts, ends []*time.Time
	errCount, runningCount := 0, 0
	types := map[string]int{}
	for _, r := range runs {
		if r["parent_run_id"] == nil || r["parent_run_id"] == "" {
			roots = append(roots, r)
		}
		if st := parseTime(r["start_time"]); st != nil {
			starts = append(starts, st)
		}
		if et := parseTime(r["end_time"]); et != nil {
			ends = append(ends, et)
		}
		if RunStatus(r) == "error" {
			errCount++
		}
		if RunStatus(r) == "running" {
			runningCount++
		}
		rt, _ := r["run_type"].(string)
		if rt == "" {
			rt = "unknown"
		}
		types[rt]++
	}
	name := "(unnamed)"
	if len(roots) > 0 {
		if n, ok := roots[0]["name"].(string); ok && n != "" {
			name = n
		}
	} else if len(runs) > 0 {
		if n, ok := runs[0]["name"].(string); ok && n != "" {
			name = n
		}
	}
	var start, end *time.Time
	for _, s := range starts {
		if start == nil || s.Before(*start) {
			start = s
		}
	}
	for _, e := range ends {
		if end == nil || e.After(*end) {
			end = e
		}
	}
	out := map[string]any{
		"trace_id":      traceID,
		"name":          name,
		"session_name":  runs[0]["session_name"],
		"run_count":     len(runs),
		"error_count":   errCount,
		"running_count": runningCount,
		"tokens":        map[string]int{"input": 0, "output": 0, "total": 0},
		"run_types":     types,
	}
	if start != nil {
		out["start"] = start.Format(time.RFC3339Nano)
	} else {
		out["start"] = nil
	}
	if end != nil {
		out["end"] = end.Format(time.RFC3339Nano)
	} else {
		out["end"] = nil
	}
	if start != nil && end != nil {
		out["duration"] = end.Sub(*start).Seconds()
	} else {
		out["duration"] = nil
	}
	return out
}

func TraceSummaries(runs []map[string]any) []map[string]any {
	grouped := GroupByTrace(runs)
	out := make([]map[string]any, 0, len(grouped))
	for tid, truns := range grouped {
		out = append(out, TraceSummary(tid, truns))
	}
	sort.Slice(out, func(i, j int) bool {
		si, _ := out[i]["start"].(string)
		sj, _ := out[j]["start"].(string)
		return si > sj
	})
	return out
}

func TraceDetail(traceID string, runs []map[string]any) map[string]any {
	if len(runs) == 0 {
		return nil
	}
	sort.SliceStable(runs, func(i, j int) bool {
		ai, as := orderKey(runs[i])
		bj, bs := orderKey(runs[j])
		if ai != bj {
			return ai < bj
		}
		return as < bs
	})
	byID := map[string]map[string]any{}
	for _, r := range runs {
		id, _ := r["id"].(string)
		byID[id] = r
	}
	var starts, ends []*time.Time
	for _, r := range runs {
		if st := parseTime(r["start_time"]); st != nil {
			starts = append(starts, st)
		}
		if et := parseTime(r["end_time"]); et != nil {
			ends = append(ends, et)
		}
	}
	if len(starts) == 0 {
		return nil
	}
	t0 := starts[0]
	for _, s := range starts {
		if s.Before(*t0) {
			t0 = s
		}
	}
	tEnd := t0
	for _, e := range ends {
		if e.After(*tEnd) {
			tEnd = e
		}
	}
	span := tEnd.Sub(*t0).Seconds()
	if span < 1e-6 {
		span = 1e-6
	}
	rows := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		start := parseTime(run["start_time"])
		end := parseTime(run["end_time"])
		var latency *float64
		if start != nil && end != nil {
			sec := end.Sub(*start).Seconds()
			latency = &sec
		}
		offset := 0.0
		if start != nil {
			offset = start.Sub(*t0).Seconds()
		}
		tags := run["tags"]
		if tags == nil {
			tags = []any{}
		}
		atts := run["attachments"]
		if atts == nil {
			atts = map[string]any{}
		}
		rows = append(rows, map[string]any{
			"id":            run["id"],
			"parent_run_id": run["parent_run_id"],
			"name":          run["name"],
			"run_type":      orDefault(run["run_type"], "chain"),
			"node":          runNode(run),
			"status":        RunStatus(run),
			"depth":         depthOf(run, byID),
			"offset":        offset,
			"latency":       latency,
			"self_time":     selfTime(run, runs),
			"start_time":    run["start_time"],
			"end_time":      run["end_time"],
			"tokens":        nil,
			"model":         runModel(run),
			"tags":          tags,
			"dotted_order":  run["dotted_order"],
			"inputs":        run["inputs"],
			"outputs":       run["outputs"],
			"extra":         run["extra"],
			"serialized":    run["serialized"],
			"error":         run["error"],
			"attachments":   atts,
		})
	}
	return map[string]any{
		"trace_id": traceID,
		"summary":  TraceSummary(traceID, runs),
		"span":     span,
		"rows":     rows,
	}
}

func orDefault(v any, def string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return def
}
