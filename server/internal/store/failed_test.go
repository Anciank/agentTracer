package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRunHasErrorAndTraceFailed(t *testing.T) {
	if RunHasError(map[string]any{"error": "boom"}) != true {
		t.Fatal("error text")
	}
	if RunHasError(map[string]any{"status": "error"}) != true {
		t.Fatal("status error")
	}
	if RunHasError(map[string]any{"status": "success", "error": ""}) != false {
		t.Fatal("success should not be failed")
	}
	if !TraceHasFailedRun([]map[string]any{
		{"status": "success"},
		{"error": "child boom", "status": "error"},
	}) {
		t.Fatal("child error should fail trace")
	}
	sql := TraceFailedExistsSQL("r.trace_id")
	if sql == "" || !containsAll(sql, "EXISTS", "__fe", "r.trace_id") {
		t.Fatalf("bad SQL: %s", sql)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !containsStr(s, p) {
			return false
		}
	}
	return true
}

func containsStr(s, p string) bool {
	return len(s) >= len(p) && (s == p || len(p) == 0 ||
		(len(s) > 0 && (func() bool {
			for i := 0; i+len(p) <= len(s); i++ {
				if s[i:i+len(p)] == p {
					return true
				}
			}
			return false
		})()))
}

// TestChildOnlyErrorCountsAsFailed: root succeeds, compressor child errors —
// Monitor error_count, list status=error, and by_model error_rate must agree.
func TestChildOnlyErrorCountsAsFailed(t *testing.T) {
	pool := testPool(t)
	st := New(pool, NewMemoryBlob())
	ctx := context.Background()
	proj := "cb2-failed-def-child"
	now := time.Now().UTC()

	must := func(payload map[string]any, op string) {
		t.Helper()
		if err := st.UpsertRun(ctx, payload, op); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	root := uuid.New()
	llm := uuid.New()
	comp := uuid.New()
	start := now.Add(-20 * time.Minute)
	end := now.Add(-18 * time.Minute)

	must(map[string]any{
		"id": root.String(), "trace_id": root.String(), "name": "LangGraph", "run_type": "chain",
		"session_name": proj, "start_time": start.Format(time.RFC3339Nano),
	}, "post")
	must(map[string]any{
		"id": llm.String(), "trace_id": root.String(), "parent_run_id": root.String(),
		"name": "agent", "run_type": "llm", "model": "stub-chat", "session_name": proj,
		"start_time":   start.Add(time.Second).Format(time.RFC3339Nano),
		"end_time":     end.Format(time.RFC3339Nano),
		"input_tokens": 10, "output_tokens": 5, "total_tokens": 15,
		"status": "success",
	}, "post")
	// Non-root compressor node errors; root will finish OK.
	must(map[string]any{
		"id": comp.String(), "trace_id": root.String(), "parent_run_id": root.String(),
		"name": "compressor", "run_type": "chain", "session_name": proj,
		"start_time": start.Add(2 * time.Second).Format(time.RFC3339Nano),
		"end_time":   end.Format(time.RFC3339Nano),
		"error":      "compressor overflow", "status": "error",
		"extra": map[string]any{"metadata": map[string]any{"langgraph_node": "compressor"}},
	}, "post")
	must(map[string]any{
		"id": root.String(), "end_time": end.Format(time.RFC3339Nano), "status": "success",
	}, "patch")

	out, _, err := st.UIMonitor(ctx, MonitorOpts{Project: proj, Window: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	if out["error_count"] != 1 {
		t.Fatalf("monitor error_count=%v want 1 (child-only)", out["error_count"])
	}
	if out["trace_count"] != 1 {
		t.Fatalf("trace_count=%v", out["trace_count"])
	}

	list, _, err := st.UIListTraces(ctx, TraceListOpts{Project: proj, Status: "error", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("list status=error len=%d want 1", len(list))
	}
	if list[0]["status"] != "error" {
		t.Fatalf("list status=%v", list[0]["status"])
	}
	if asInt(list[0]["error_count"]) < 1 {
		t.Fatalf("list error_count=%v", list[0]["error_count"])
	}

	models, _ := out["by_model"].([]map[string]any)
	found := false
	for _, m := range models {
		if m["model"] == "stub-chat" {
			found = true
			if asInt(m["error_count"]) < 1 {
				t.Fatalf("by_model error_count=%v want >=1 for stub-chat in failed trace", m["error_count"])
			}
			if asFloat(m["error_rate"]) <= 0 {
				t.Fatalf("by_model error_rate=%v want >0", m["error_rate"])
			}
		}
	}
	if !found {
		t.Fatalf("stub-chat missing from by_model: %v", models)
	}

	stats, err := st.UIStats(ctx, proj, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if asFloat(stats["error_rate"]) <= 0 {
		t.Fatalf("UIStats error_rate=%v", stats["error_rate"])
	}
}

func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	default:
		return 0
	}
}
