package store

import (
	"context"
	"testing"

	"github.com/Anciank/agentTracer/server/internal/traces"
	"github.com/google/uuid"
)

func segment(ts, id string) string {
	return ts + id
}

func TestRootTraceIDFromDottedOrder(t *testing.T) {
	root := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	child := "11111111-2222-3333-4444-555555555555"
	ts := "20260115T120304056789Z"
	dotted := segment(ts, root) + "." + segment("20260115T120304100000Z", child)
	got := rootTraceIDFromDottedOrder(dotted)
	if got == nil || got.String() != root {
		t.Fatalf("got %v want %s", got, root)
	}
	if rootTraceIDFromDottedOrder("") != nil {
		t.Fatal("empty should be nil")
	}
	if rootTraceIDFromDottedOrder("short") != nil {
		t.Fatal("too short should be nil")
	}
}

func TestResolveTraceIDChildEqualsOwnIDUsesDottedOrder(t *testing.T) {
	root := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	child := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	ts := "20260115T120304056789Z"
	dotted := segment(ts, root.String()) + "." + segment("20260115T120304100000Z", child.String())

	// SDK bug shape: child.trace_id == child.id, parent set, dotted_order present.
	merged := map[string]any{
		"id":            child.String(),
		"trace_id":      child.String(),
		"parent_run_id": root.String(),
		"dotted_order":  dotted,
	}
	var s *Store // nil store: must not need parent lookup when dotted_order present
	got := s.resolveTraceID(context.Background(), child, merged)
	if got == nil || *got != root {
		t.Fatalf("got %v want root %s", got, root)
	}
}

func TestResolveTraceIDMissingUsesDottedOrder(t *testing.T) {
	root := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	child := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	ts := "20260115T120304056789Z"
	dotted := segment(ts, root.String()) + "." + segment("20260115T120304100000Z", child.String())
	merged := map[string]any{
		"id":            child.String(),
		"parent_run_id": root.String(),
		"dotted_order":  dotted,
	}
	var s *Store
	got := s.resolveTraceID(context.Background(), child, merged)
	if got == nil || *got != root {
		t.Fatalf("got %v want %s", got, root)
	}
}

func TestResolveTraceIDRootKeepsOwnID(t *testing.T) {
	root := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	ts := "20260115T120304056789Z"
	merged := map[string]any{
		"id":           root.String(),
		"trace_id":     root.String(),
		"dotted_order": segment(ts, root.String()),
	}
	var s *Store
	got := s.resolveTraceID(context.Background(), root, merged)
	if got == nil || *got != root {
		t.Fatalf("got %v", got)
	}
}

func TestOutOfOrderChildBeforeParentGroupsUnderRoot(t *testing.T) {
	// Simulate ingest order: child first (with SDK-buggy trace_id==id), then root.
	// After resolveTraceID, both share the root UUID as trace_id; TraceDetail
	// groups them with root at depth 0.
	rootID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	childID := "11111111-2222-3333-4444-555555555555"
	tsRoot := "20260115T120304000000Z"
	tsChild := "20260115T120304050000Z"
	rootDotted := segment(tsRoot, rootID)
	childDotted := rootDotted + "." + segment(tsChild, childID)

	var s *Store
	childMerged := map[string]any{
		"id": childID, "trace_id": childID, "parent_run_id": rootID,
		"dotted_order": childDotted, "name": "ChatOpenAI", "run_type": "llm",
		"start_time": "2026-01-15T12:03:04.050000+00:00",
	}
	childTrace := s.resolveTraceID(context.Background(), uuid.MustParse(childID), childMerged)
	if childTrace == nil || childTrace.String() != rootID {
		t.Fatalf("child trace_id=%v want %s", childTrace, rootID)
	}

	rootMerged := map[string]any{
		"id": rootID, "trace_id": rootID, "dotted_order": rootDotted,
		"name": "RunnableSequence", "run_type": "chain",
		"start_time": "2026-01-15T12:03:04.000000+00:00",
		"end_time":   "2026-01-15T12:03:05.000000+00:00",
	}
	rootTrace := s.resolveTraceID(context.Background(), uuid.MustParse(rootID), rootMerged)
	if rootTrace == nil || rootTrace.String() != rootID {
		t.Fatalf("root trace_id=%v", rootTrace)
	}

	runs := []map[string]any{
		{
			"id": childID, "trace_id": childTrace.String(), "parent_run_id": rootID,
			"name": "ChatOpenAI", "run_type": "llm", "dotted_order": childDotted,
			"start_time": "2026-01-15T12:03:04.050000+00:00",
			"end_time":   "2026-01-15T12:03:04.900000+00:00",
		},
		{
			"id": rootID, "trace_id": rootTrace.String(),
			"name": "RunnableSequence", "run_type": "chain", "dotted_order": rootDotted,
			"start_time": "2026-01-15T12:03:04.000000+00:00",
			"end_time":   "2026-01-15T12:03:05.000000+00:00",
		},
	}
	detail := traces.TraceDetail(rootID, runs)
	rows, _ := detail["rows"].([]map[string]any)
	if len(rows) != 2 {
		t.Fatalf("rows=%d detail=%v", len(rows), detail)
	}
	// Root must be depth 0; child deeper.
	var rootDepth, childDepth int = -1, -1
	for _, r := range rows {
		switch r["id"] {
		case rootID:
			rootDepth = r["depth"].(int)
		case childID:
			childDepth = r["depth"].(int)
		}
	}
	if rootDepth != 0 {
		t.Fatalf("root depth=%d want 0", rootDepth)
	}
	if childDepth != 1 {
		t.Fatalf("child depth=%d want 1", childDepth)
	}
	sum, _ := detail["summary"].(map[string]any)
	if sum["trace_id"] != rootID {
		t.Fatalf("summary trace_id=%v", sum["trace_id"])
	}
	if n, _ := sum["run_count"].(int); n != 2 {
		t.Fatalf("run_count=%v", sum["run_count"])
	}
}
