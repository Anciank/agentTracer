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
