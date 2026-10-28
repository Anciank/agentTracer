package store

import (
	"context"
	"testing"
	"time"

	"github.com/Anciank/agentTracer/server/internal/graph"
	"github.com/google/uuid"
)

func TestUIGraphPathsAndFingerprint(t *testing.T) {
	pool := testPool(t)
	st := New(pool, NewMemoryBlob())
	ctx := context.Background()
	proj := "m35-graph-test"
	now := time.Now().UTC()

	mkTrace := func(nodes []string, failAt int) string {
		root := uuid.NewString()
		ts := now.Format("20060102T150405.000000Z")
		ts = ts[:15] + "000000Z" // keep dotted_order-ish
		_ = ts
		rootStart := now.Add(-2 * time.Minute).Format(time.RFC3339Nano)
		if err := st.UpsertRun(ctx, map[string]any{
			"id": root, "trace_id": root, "name": "LangGraph", "run_type": "chain",
			"session_name": proj, "start_time": rootStart,
		}, "post"); err != nil {
			t.Fatal(err)
		}
		for i, n := range nodes {
			cid := uuid.NewString()
			stt := now.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano)
			payload := map[string]any{
				"id": cid, "trace_id": root, "parent_run_id": root,
				"name": n, "run_type": "chain", "session_name": proj,
				"start_time": stt, "end_time": now.Add(time.Duration(i)*time.Second + 500*time.Millisecond).Format(time.RFC3339Nano),
				"extra": map[string]any{"metadata": map[string]any{
					"langgraph_node": n, "langgraph_step": i + 1,
				}},
			}
			if failAt == i {
				payload["error"] = "boom"
				payload["status"] = "error"
			}
			if err := st.UpsertRun(ctx, payload, "post"); err != nil {
				t.Fatal(err)
			}
		}
		// End root → fingerprint
		if err := st.UpsertRun(ctx, map[string]any{
			"id": root, "end_time": now.Add(5 * time.Minute).Format(time.RFC3339Nano),
			"status": "success",
		}, "patch"); err != nil {
			t.Fatal(err)
		}
		return root
	}

	a := mkTrace([]string{"supervisor", "tools", "tools", "tools", "respond"}, -1)
	b := mkTrace([]string{"supervisor", "respond"}, -1)
	c := mkTrace([]string{"supervisor", "tools", "respond"}, 1) // tools fails
	_, _, _ = a, b, c

	wantFP := "supervisor>tools×3>respond"
	rootA, err := st.GetRun(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if asString(rootA["path_fingerprint"]) != wantFP {
		t.Fatalf("GetRun path_fingerprint=%v want %q", rootA["path_fingerprint"], wantFP)
	}
	// Ending the root must also write trace_graph_cache.
	var cacheKeys []string
	if err := pool.QueryRow(ctx, `SELECT node_keys FROM trace_graph_cache WHERE trace_id=$1`, uuid.MustParse(a)).Scan(&cacheKeys); err != nil {
		t.Fatalf("trace_graph_cache missing: %v", err)
	}
	if len(cacheKeys) < 2 || cacheKeys[0] != "supervisor" {
		t.Fatalf("cache keys=%v", cacheKeys)
	}
	// Non-root / unfinished: null
	var childID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM runs WHERE trace_id=$1 AND parent_run_id IS NOT NULL LIMIT 1`, uuid.MustParse(a)).Scan(&childID); err != nil {
		t.Fatal(err)
	}
	child, err := st.GetRun(ctx, childID)
	if err != nil {
		t.Fatal(err)
	}
	if child["path_fingerprint"] != nil {
		t.Fatalf("child path_fingerprint should be nil, got %v", child["path_fingerprint"])
	}

	from := now.Add(-1 * time.Hour)
	to := now.Add(1 * time.Hour)
	g, err := st.UIGraph(ctx, GraphOpts{Project: proj, From: &from, To: &to, TopN: 15, MinEdgeShare: 0.01})
	if err != nil {
		t.Fatal(err)
	}
	if g["trace_count"].(int) != 3 {
		t.Fatalf("trace_count=%v", g["trace_count"])
	}
	nodes := g["nodes"].([]map[string]any)
	keys := map[string]bool{}
	for _, n := range nodes {
		keys[n["key"].(string)] = true
		if n["key"] == "tools" && asInt(n["error_count"]) < 1 {
			t.Fatalf("tools should have error: %v", n)
		}
	}
	for _, k := range []string{"supervisor", "tools", "respond"} {
		if !keys[k] {
			t.Fatalf("missing node %s in %v", k, keys)
		}
	}
	edges := g["edges"].([]map[string]any)
	foundLoop := false
	for _, e := range edges {
		if e["from"] == "tools" && e["to"] == "tools" {
			foundLoop = true
			if e["loop"] != true {
				t.Fatalf("loop flag missing: %v", e)
			}
		}
	}
	if !foundLoop {
		t.Fatalf("expected tools self-loop among %v", edges)
	}

	paths, err := st.UIPaths(ctx, PathsOpts{Project: proj, From: &from, To: &to, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	plist := paths["paths"].([]map[string]any)
	if len(plist) < 2 {
		t.Fatalf("paths=%v", plist)
	}
	found := false
	for _, p := range plist {
		if p["fingerprint"] == wantFP {
			found = true
			if asInt(p["count"]) != 1 {
				t.Fatalf("count for loop path: %v", p)
			}
		}
	}
	if !found {
		t.Fatalf("missing fingerprint in paths: %v", plist)
	}

	// Filters
	list, _, err := st.UIListTraces(ctx, TraceListOpts{Project: proj, Path: wantFP, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("path filter got %d", len(list))
	}
	list, _, err = st.UIListTraces(ctx, TraceListOpts{Project: proj, Node: "tools", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("node=tools got %d", len(list))
	}
	list, _, err = st.UIListTraces(ctx, TraceListOpts{Project: proj, NodeFail: "tools", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("node_fail=tools got %d", len(list))
	}
	list, _, err = st.UIListTraces(ctx, TraceListOpts{Project: proj, Edge: "supervisor>tools", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("edge filter got %d", len(list))
	}

	// Late child PATCH refreshes fingerprint
	late := uuid.NewString()
	if err := st.UpsertRun(ctx, map[string]any{
		"id": late, "trace_id": a, "parent_run_id": a,
		"name": "audit", "run_type": "chain", "session_name": proj,
		"start_time": now.Add(4 * time.Minute).Format(time.RFC3339Nano),
		"end_time":   now.Add(4*time.Minute + time.Second).Format(time.RFC3339Nano),
		"extra": map[string]any{"metadata": map[string]any{
			"langgraph_node": "audit", "langgraph_step": 99,
		}},
	}, "post"); err != nil {
		t.Fatal(err)
	}
	rootA, err = st.GetRun(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if asString(rootA["path_fingerprint"]) != "supervisor>tools×3>respond>audit" {
		t.Fatalf("late child refresh fp=%v", rootA["path_fingerprint"])
	}
	if err := pool.QueryRow(ctx, `SELECT node_keys FROM trace_graph_cache WHERE trace_id=$1`, uuid.MustParse(a)).Scan(&cacheKeys); err != nil {
		t.Fatalf("cache after late child: %v", err)
	}
	if cacheKeys[len(cacheKeys)-1] != "audit" {
		t.Fatalf("cache not refreshed with audit: %v", cacheKeys)
	}
	n, err := st.BackfillGraphCache(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("backfill should be noop when complete, got %d", n)
	}
	_ = graph.OtherKey
}

func TestParseWindow(t *testing.T) {
	_, _, w, err := parseWindow("7d", nil, nil)
	if err != nil || w != "7d" {
		t.Fatalf("%v %v", w, err)
	}
	_, _, _, err = parseWindow("nope", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestUIGraphFanoutLatencyAndUnended: parallel same-key visits contribute
// latency; a visit without end_time is counted but excluded from p50/p95;
// sub-millisecond finished visits still yield non-null latency stats.
func TestUIGraphFanoutLatencyAndUnended(t *testing.T) {
	pool := testPool(t)
	st := New(pool, NewMemoryBlob())
	ctx := context.Background()
	proj := "m35-fanout-lat"
	now := time.Now().UTC()
	root := uuid.NewString()
	rootStart := now.Add(-time.Minute)

	if err := st.UpsertRun(ctx, map[string]any{
		"id": root, "trace_id": root, "name": "LangGraph", "run_type": "chain",
		"session_name": proj, "start_time": rootStart.Format(time.RFC3339Nano),
	}, "post"); err != nil {
		t.Fatal(err)
	}

	// plan: finished but sub-millisecond (start==end) — must still count as latency
	planID := uuid.NewString()
	planT := rootStart.Add(10 * time.Millisecond)
	if err := st.UpsertRun(ctx, map[string]any{
		"id": planID, "trace_id": root, "parent_run_id": root,
		"name": "plan", "run_type": "chain", "session_name": proj,
		"start_time": planT.Format(time.RFC3339Nano),
		"end_time":   planT.Format(time.RFC3339Nano), // 0ms
		"extra":      map[string]any{"metadata": map[string]any{"langgraph_node": "plan", "langgraph_step": 1}},
	}, "post"); err != nil {
		t.Fatal(err)
	}

	// worker fan-out: three parallel finished instances with distinct latencies
	workerLats := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 300 * time.Millisecond}
	for i, d := range workerLats {
		id := uuid.NewString()
		stt := rootStart.Add(time.Duration(20+i) * time.Millisecond)
		if err := st.UpsertRun(ctx, map[string]any{
			"id": id, "trace_id": root, "parent_run_id": root,
			"name": "worker", "run_type": "chain", "session_name": proj,
			"start_time": stt.Format(time.RFC3339Nano),
			"end_time":   stt.Add(d).Format(time.RFC3339Nano),
			"extra": map[string]any{"metadata": map[string]any{
				"langgraph_node": "worker", "langgraph_step": 2,
			}},
		}, "post"); err != nil {
			t.Fatal(err)
		}
	}

	// retrieve: started but never ended — counted, no latency contribution
	retID := uuid.NewString()
	if err := st.UpsertRun(ctx, map[string]any{
		"id": retID, "trace_id": root, "parent_run_id": root,
		"name": "retrieve", "run_type": "chain", "session_name": proj,
		"start_time": rootStart.Add(50 * time.Millisecond).Format(time.RFC3339Nano),
		"extra":      map[string]any{"metadata": map[string]any{"langgraph_node": "retrieve", "langgraph_step": 3}},
	}, "post"); err != nil {
		t.Fatal(err)
	}

	// End root → cache write
	if err := st.UpsertRun(ctx, map[string]any{
		"id": root, "end_time": now.Format(time.RFC3339Nano), "status": "success",
	}, "patch"); err != nil {
		t.Fatal(err)
	}

	// Cache arrays aligned: lat sentinel -1 for unended retrieve
	var keys []string
	var lats []float64
	if err := pool.QueryRow(ctx,
		`SELECT node_keys, node_lat_ms FROM trace_graph_cache WHERE trace_id=$1`,
		uuid.MustParse(root),
	).Scan(&keys, &lats); err != nil {
		t.Fatalf("cache: %v", err)
	}
	if len(keys) != len(lats) {
		t.Fatalf("misaligned keys=%d lats=%d", len(keys), len(lats))
	}
	foundPlan, foundWorker, foundRet := 0, 0, 0
	for i, k := range keys {
		switch k {
		case "plan":
			foundPlan++
			if lats[i] < 0 {
				t.Fatalf("plan finished 0ms should be >=0, got %v", lats[i])
			}
		case "worker":
			foundWorker++
			if lats[i] < 0 {
				t.Fatalf("worker should be finished, lat=%v", lats[i])
			}
		case "retrieve":
			foundRet++
			if lats[i] != latMissing {
				t.Fatalf("retrieve unended want %v got %v", latMissing, lats[i])
			}
		}
	}
	if foundPlan != 1 || foundWorker != 3 || foundRet != 1 {
		t.Fatalf("keys=%v plan=%d worker=%d retrieve=%d", keys, foundPlan, foundWorker, foundRet)
	}

	from := now.Add(-time.Hour)
	to := now.Add(time.Hour)
	g, err := st.UIGraph(ctx, GraphOpts{Project: proj, From: &from, To: &to, TopN: 15, MinEdgeShare: 0.01})
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]map[string]any{}
	for _, n := range g["nodes"].([]map[string]any) {
		byKey[n["key"].(string)] = n
	}
	plan := byKey["plan"]
	if plan == nil || plan["p50_ms"] == nil || plan["p95_ms"] == nil {
		t.Fatalf("plan (0ms finished) want non-null p50/p95, got %v", plan)
	}
	worker := byKey["worker"]
	if worker == nil || asInt(worker["count"]) != 3 {
		t.Fatalf("worker count: %v", worker)
	}
	if worker["p50_ms"] == nil || worker["p95_ms"] == nil {
		t.Fatalf("worker fan-out want non-null latency, got %v", worker)
	}
	// p50 of 100,200,300 ≈ 200
	if asInt(worker["p50_ms"]) < 100 || asInt(worker["p50_ms"]) > 300 {
		t.Fatalf("worker p50 unexpected: %v", worker["p50_ms"])
	}
	ret := byKey["retrieve"]
	if ret == nil || asInt(ret["count"]) != 1 {
		t.Fatalf("retrieve count: %v", ret)
	}
	if ret["p50_ms"] != nil || ret["p95_ms"] != nil {
		t.Fatalf("retrieve unended want null latency, got %v", ret)
	}
}
