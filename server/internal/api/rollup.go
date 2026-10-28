package api

import (
	"strings"
	"time"
)

// fillNullLLMRollup fills unset token/model fields on run from its LLM
// descendants in the same trace (already loaded in traceRuns).
//
// Rules:
//   - Only fill when the run's own value is unset: tokens == 0, model nil/"".
//   - Never overwrite a non-zero token field or a non-empty model.
//   - Descendants = all runs reachable via parent_run_id under run (any depth),
//     filtered to run_type=llm (case-insensitive). The run itself is excluded.
//   - Token fills are the sums of input_tokens / output_tokens / total_tokens
//     across those LLM descendants.
//   - Model: if exactly one distinct non-empty model among LLM descendants, use
//     it. If several, pick the most frequent; ties break to the model whose
//     earliest start_time is first (stable, documented choice).
//
// Returns (input, output, total, model) ready for the run-detail response.
// model may be nil when still unset after rollup.
//
// Efficient: callers pass the same traceRuns slice already fetched for
// self_time (QueryRuns by trace_id / idx_runs_trace); no extra DB round-trip.
func fillNullLLMRollup(run map[string]any, traceRuns []map[string]any) (in, out, tot int, model any) {
	in = asIntAny(run["input_tokens"])
	out = asIntAny(run["output_tokens"])
	tot = asIntAny(run["total_tokens"])
	model = run["model"]
	if !tokenUnset(in) && !tokenUnset(out) && !tokenUnset(tot) && !modelUnset(model) {
		return in, out, tot, normalizeModel(model)
	}

	llms := llmDescendants(run, traceRuns)
	if len(llms) == 0 {
		return in, out, tot, normalizeModel(model)
	}

	var sumIn, sumOut, sumTot int
	for _, c := range llms {
		sumIn += asIntAny(c["input_tokens"])
		sumOut += asIntAny(c["output_tokens"])
		sumTot += asIntAny(c["total_tokens"])
	}
	if tokenUnset(in) {
		in = sumIn
	}
	if tokenUnset(out) {
		out = sumOut
	}
	if tokenUnset(tot) {
		tot = sumTot
	}
	if modelUnset(model) {
		model = pickModelFromLLMs(llms)
	}
	return in, out, tot, normalizeModel(model)
}

func tokenUnset(v int) bool { return v == 0 }

func modelUnset(v any) bool {
	if v == nil {
		return true
	}
	return strings.TrimSpace(asStr(v)) == ""
}

func normalizeModel(v any) any {
	if modelUnset(v) {
		return nil
	}
	return asStr(v)
}

// llmDescendants returns run_type=llm runs under run via parent_run_id (any depth).
func llmDescendants(run map[string]any, traceRuns []map[string]any) []map[string]any {
	rootID := asStr(run["id"])
	if rootID == "" || len(traceRuns) == 0 {
		return nil
	}
	byParent := map[string][]map[string]any{}
	for _, r := range traceRuns {
		pid := asStr(r["parent_run_id"])
		if pid == "" {
			continue
		}
		byParent[pid] = append(byParent[pid], r)
	}
	var out []map[string]any
	queue := []string{rootID}
	seen := map[string]bool{rootID: true}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, child := range byParent[id] {
			cid := asStr(child["id"])
			if cid == "" || seen[cid] {
				continue
			}
			seen[cid] = true
			queue = append(queue, cid)
			if strings.EqualFold(asStr(child["run_type"]), "llm") {
				out = append(out, child)
			}
		}
	}
	return out
}

// pickModelFromLLMs: single distinct model wins; else most-used, tie-break earliest start_time.
func pickModelFromLLMs(llms []map[string]any) any {
	type info struct {
		count int
		first time.Time
		hasT  bool
	}
	stats := map[string]*info{}
	for _, c := range llms {
		m := strings.TrimSpace(asStr(c["model"]))
		if m == "" {
			continue
		}
		st, ok := stats[m]
		if !ok {
			st = &info{}
			stats[m] = st
		}
		st.count++
		if t := parseTimeAny(c["start_time"]); t != nil {
			if !st.hasT || t.Before(st.first) {
				st.first = *t
				st.hasT = true
			}
		}
	}
	if len(stats) == 0 {
		return nil
	}
	if len(stats) == 1 {
		for m := range stats {
			return m
		}
	}
	best := ""
	var bestInfo *info
	for m, st := range stats {
		if bestInfo == nil {
			best, bestInfo = m, st
			continue
		}
		if st.count > bestInfo.count {
			best, bestInfo = m, st
			continue
		}
		if st.count < bestInfo.count {
			continue
		}
		// tie on count: earliest first start_time wins; missing time sorts last
		switch {
		case st.hasT && !bestInfo.hasT:
			best, bestInfo = m, st
		case st.hasT && bestInfo.hasT && st.first.Before(bestInfo.first):
			best, bestInfo = m, st
		case st.hasT && bestInfo.hasT && st.first.Equal(bestInfo.first) && m < best:
			best, bestInfo = m, st
		case !st.hasT && !bestInfo.hasT && m < best:
			best, bestInfo = m, st
		}
	}
	if best == "" {
		return nil
	}
	return best
}
