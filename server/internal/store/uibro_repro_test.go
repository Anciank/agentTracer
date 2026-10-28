package store

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Anciank/agentTracer/server/internal/graph"
)

// TestUIBroA_RolledKeysOnOther: /graph __other__.rolled_keys must be a non-null
// string array when >TopN distinct nodes exist (UIBro suspected null with 26 nodes).
func TestUIBroA_RolledKeysOnOther(t *testing.T) {
	pool := testPool(t)
	st := New(pool, NewMemoryBlob())
	ctx := context.Background()
	proj := fmt.Sprintf("uibro-a-%s", uuid.New().String()[:8])
	now := time.Now().UTC()
	from := now.Add(-2 * time.Hour)
	to := now.Add(time.Hour)

	must := func(payload map[string]any, op string) {
		t.Helper()
		if err := st.UpsertRun(ctx, payload, op); err != nil {
			t.Fatalf("upsert %v: %v", payload["name"], err)
		}
	}

	root := uuid.New()
	start := now.Add(-30 * time.Minute)
	end := now.Add(-25 * time.Minute)
	must(map[string]any{
		"id": root.String(), "trace_id": root.String(), "name": "LangGraph", "run_type": "chain",
		"session_name": proj, "start_time": start.Format(time.RFC3339Nano),
	}, "post")

	const nNodes = 26
	var prev string
	for i := 0; i < nNodes; i++ {
		id := uuid.New()
		name := fmt.Sprintf("node_%02d", i)
		payload := map[string]any{
			"id": id.String(), "trace_id": root.String(), "parent_run_id": root.String(),
			"name": name, "run_type": "chain", "session_name": proj,
			"langgraph_node": name, "langgraph_step": i + 1,
			"start_time": start.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano),
			"end_time":   start.Add(time.Duration(i+1) * time.Second).Format(time.RFC3339Nano),
			"extra":      map[string]any{"langgraph": map[string]any{"node": name, "step": i + 1}},
		}
		if prev != "" {
			// keep all as direct children of root so VisitKey accepts them
			payload["parent_run_id"] = root.String()
		}
		must(payload, "post")
		prev = id.String()
		_ = prev
	}
	must(map[string]any{
		"id": root.String(), "end_time": end.Format(time.RFC3339Nano), "status": "success",
	}, "patch")

	g, err := st.UIGraph(ctx, GraphOpts{Project: proj, From: &from, To: &to, TopN: 15, MinEdgeShare: 0.0})
	if err != nil {
		t.Fatal(err)
	}

	// JSON round-trip as the HTTP client sees it
	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	t.Logf("graph JSON snippet (nodes count + other): trace_count=%v", decoded["trace_count"])

	nodesAny, _ := decoded["nodes"].([]any)
	t.Logf("decoded node count=%d", len(nodesAny))
	var other map[string]any
	var keys []string
	for _, x := range nodesAny {
		n := x.(map[string]any)
		keys = append(keys, fmt.Sprint(n["key"]))
		if n["key"] == graph.OtherKey || n["kind"] == "other" {
			other = n
		}
	}
	if other == nil {
		t.Fatalf("expected __other__ among %v", keys)
	}
	snippet, _ := json.Marshal(other)
	t.Logf("__other__ JSON: %s", snippet)

	rk, ok := other["rolled_keys"]
	if !ok || rk == nil {
		t.Fatalf("REAL BUG (a): rolled_keys missing/null on __other__: %v", other)
	}
	rkArr, ok := rk.([]any)
	if !ok || len(rkArr) == 0 {
		t.Fatalf("REAL BUG (a): rolled_keys not a non-empty array: %T %v", rk, rk)
	}
	if len(rkArr) != nNodes-15 {
		t.Fatalf("rolled_keys len=%d want %d (26-15)", len(rkArr), nNodes-15)
	}
	// Also confirm Go-native path
	nodesGo := g["nodes"].([]map[string]any)
	for _, n := range nodesGo {
		if n["key"] == graph.OtherKey {
			rk2, ok := n["rolled_keys"].([]string)
			if !ok || len(rk2) == 0 {
				t.Fatalf("Go path rolled_keys bad: %T %v", n["rolled_keys"], n["rolled_keys"])
			}
		}
	}
}

// TestUIBroB_SeriesTracesTotals: seeded windows must have series_traces[*].total
// summing to trace_count. There is no series_trace_n response field.
func TestUIBroB_SeriesTracesTotals(t *testing.T) {
	pool := testPool(t)
	st := New(pool, NewMemoryBlob())
	ctx := context.Background()
	proj := fmt.Sprintf("uibro-b-%s", uuid.New().String()[:8])
	now := time.Now().UTC()

	must := func(payload map[string]any, op string) {
		t.Helper()
		if err := st.UpsertRun(ctx, payload, op); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	const n = 5
	for i := 0; i < n; i++ {
		root := uuid.New()
		start := now.Add(-time.Duration(10+i) * time.Minute)
		end := start.Add(30 * time.Second)
		must(map[string]any{
			"id": root.String(), "trace_id": root.String(), "name": "LangGraph", "run_type": "chain",
			"session_name": proj, "start_time": start.Format(time.RFC3339Nano),
		}, "post")
		must(map[string]any{
			"id": root.String(), "end_time": end.Format(time.RFC3339Nano), "status": "success",
		}, "patch")
	}

	out, _, err := st.UIMonitor(ctx, MonitorOpts{Project: proj, Window: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{
		"trace_count":        out["trace_count"],
		"series_traces":      out["series_traces"],
		"has_series_trace_n": func() bool { _, ok := out["series_trace_n"]; return ok }(),
	})
	// Trim series for log: only non-zero buckets
	series := out["series_traces"].([]map[string]any)
	var nonzero []map[string]any
	sum := 0
	for _, b := range series {
		tot := b["total"].(int)
		sum += tot
		if tot > 0 {
			nonzero = append(nonzero, b)
		}
	}
	snippet, _ := json.Marshal(map[string]any{
		"trace_count":      out["trace_count"],
		"series_len":       len(series),
		"series_sum_total": sum,
		"nonzero_buckets":  nonzero,
		"series_trace_n_key": func() any {
			v, ok := out["series_trace_n"]
			if ok {
				return v
			}
			return "<absent>"
		}(),
	})
	t.Logf("monitor series evidence: %s", snippet)
	_ = raw

	if out["trace_count"] != n {
		t.Fatalf("trace_count=%v want %d", out["trace_count"], n)
	}
	if sum != n {
		t.Fatalf("REAL BUG (b): series_traces sum total=%d want %d", sum, n)
	}
	if _, ok := out["series_trace_n"]; ok {
		t.Fatalf("unexpected series_trace_n field present: %v", out["series_trace_n"])
	}
}

// TestUIBroC_ByToolTokenSplit: by_tool must not hardcode input/output=0 when
// total_tokens > 0; use real SUM(input/output/total).
func TestUIBroC_ByToolTokenSplit(t *testing.T) {
	pool := testPool(t)
	st := New(pool, NewMemoryBlob())
	ctx := context.Background()
	proj := fmt.Sprintf("uibro-c-%s", uuid.New().String()[:8])
	now := time.Now().UTC()

	must := func(payload map[string]any, op string) {
		t.Helper()
		if err := st.UpsertRun(ctx, payload, op); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	root := uuid.New()
	toolTok := uuid.New()
	toolBare := uuid.New()
	toolOnlyTot := uuid.New()
	llm := uuid.New()
	start := now.Add(-20 * time.Minute)
	end := now.Add(-15 * time.Minute)

	must(map[string]any{
		"id": root.String(), "trace_id": root.String(), "name": "LangGraph", "run_type": "chain",
		"session_name": proj, "start_time": start.Format(time.RFC3339Nano),
	}, "post")
	must(map[string]any{
		"id": llm.String(), "trace_id": root.String(), "parent_run_id": root.String(),
		"name": "llm", "run_type": "llm", "model": "fake-gpt", "session_name": proj,
		"start_time":   start.Add(time.Second).Format(time.RFC3339Nano),
		"end_time":     end.Format(time.RFC3339Nano),
		"input_tokens": 100, "output_tokens": 40, "total_tokens": 140,
	}, "post")
	// Tool with full token split (unusual but valid)
	must(map[string]any{
		"id": toolTok.String(), "trace_id": root.String(), "parent_run_id": root.String(),
		"name": "web_search", "run_type": "tool", "session_name": proj,
		"start_time":   start.Add(2 * time.Second).Format(time.RFC3339Nano),
		"end_time":     end.Format(time.RFC3339Nano),
		"input_tokens": 10, "output_tokens": 5, "total_tokens": 15,
	}, "post")
	// Tool with only total_tokens (no in/out)
	must(map[string]any{
		"id": toolOnlyTot.String(), "trace_id": root.String(), "parent_run_id": root.String(),
		"name": "embed_tool", "run_type": "tool", "session_name": proj,
		"start_time":   start.Add(3 * time.Second).Format(time.RFC3339Nano),
		"end_time":     end.Format(time.RFC3339Nano),
		"total_tokens": 77,
	}, "post")
	// Tool with no tokens
	must(map[string]any{
		"id": toolBare.String(), "trace_id": root.String(), "parent_run_id": root.String(),
		"name": "noop", "run_type": "tool", "session_name": proj,
		"start_time": start.Add(4 * time.Second).Format(time.RFC3339Nano),
		"end_time":   end.Format(time.RFC3339Nano),
	}, "post")
	must(map[string]any{
		"id": root.String(), "end_time": end.Format(time.RFC3339Nano), "status": "success",
	}, "patch")

	out, _, err := st.UIMonitor(ctx, MonitorOpts{Project: proj, Window: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	tools := out["by_tool"].([]map[string]any)
	models := out["by_model"].([]map[string]any)
	seriesTok := out["series_tokens"].([]map[string]any)
	snippet, _ := json.Marshal(map[string]any{
		"by_tool": tools, "by_model": models,
		"header_tokens": out["tokens"],
	})
	t.Logf("token evidence: %s", snippet)

	byName := map[string]map[string]any{}
	for _, tr := range tools {
		byName[tr["tool"].(string)] = tr
	}

	// (c) current bug: web_search would show input:0 output:0 total:15
	ws := byName["web_search"]
	if ws == nil {
		t.Fatal("missing web_search")
	}
	tok, _ := ws["tokens"].(map[string]any)
	snippetWS, _ := json.Marshal(ws)
	t.Logf("web_search: %s", snippetWS)
	if tok == nil {
		t.Fatal("web_search tokens unexpectedly null")
	}
	in, outn, tot := asInt(tok["input"]), asInt(tok["output"]), asInt(tok["total"])
	if in != 10 || outn != 5 || tot != 15 {
		t.Fatalf("web_search tokens want {10,5,15} got %v", tok)
	}

	noop := byName["noop"]
	if noop["tokens"] != nil {
		t.Fatalf("noop tokens want null got %v", noop["tokens"])
	}

	// embed_tool: only total_tokens set — input/output stay 0 (no invented split)
	et := byName["embed_tool"]
	etTok, _ := et["tokens"].(map[string]any)
	t.Logf("embed_tool (total-only): %v", etTok)
	if etTok == nil || asInt(etTok["total"]) != 77 || asInt(etTok["input"]) != 0 || asInt(etTok["output"]) != 0 {
		t.Fatalf("embed_tool want {0,0,77} got %v", etTok)
	}

	// by_model should already have real splits
	m0 := models[0]
	mt, _ := m0["tokens"].(map[string]any)
	if asInt(mt["input"]) == 0 && asInt(mt["total"]) > 0 {
		t.Fatalf("by_model also broken: %v", mt)
	}

	// series_tokens should sum header llm tokens (tools not included in series llm lateral)
	var sIn, sOut, sTot int64
	for _, b := range seriesTok {
		sIn += b["input"].(int64)
		sOut += b["output"].(int64)
		sTot += b["total"].(int64)
	}
	ht, _ := out["tokens"].(map[string]any)
	if sTot != int64(asInt(ht["total"])) {
		t.Fatalf("series_tokens total sum %d vs header %v", sTot, ht)
	}
	_ = sIn
	_ = sOut
}
