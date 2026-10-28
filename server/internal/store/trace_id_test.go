package store

import (
	"context"
	"encoding/json"
	"os"
	"strings"
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

// uibroDottedPart mirrors /tmp/ingest_m3_final.py dotted_part:
// compact UTC stamp + undashed first 8 hex of the uuid.
func uibroDottedPart(ts, id string) string {
	hex := strings.ReplaceAll(id, "-", "")
	if len(hex) > 8 {
		hex = hex[:8]
	}
	return ts + hex
}

func TestRootTraceIDFromDottedOrderShortHexIsNil(t *testing.T) {
	// Short hex alone must NOT parse as a UUID (that was the UIBro miss).
	ts := "20261028T074333745856Z"
	root := "d23b9ff8-0793-43c5-8542-4e71baed406a"
	child := "093bdce7-39e0-46c9-8698-18158d893090"
	dotted := uibroDottedPart(ts, root) + "." + uibroDottedPart("20261028T074333695856Z", child)
	if got := rootTraceIDFromDottedOrder(dotted); got != nil {
		t.Fatalf("short-hex dotted_order must not parse as UUID, got %v (dotted=%q)", got, dotted)
	}
}

func TestResolveTraceIDUIBroShortHexChildBeforeParent(t *testing.T) {
	// Exact UIBro shape from ingest_m3_final.py / oo_child_run.json:
	// create_run(early_child_llm) BEFORE oo_parent_chain, no trace_id arg,
	// parent_run_id=root, dotted_order uses undashed[:8] suffixes.
	root := uuid.MustParse("d23b9ff8-0793-43c5-8542-4e71baed406a")
	child := uuid.MustParse("093bdce7-39e0-46c9-8698-18158d893090")
	tsRoot := "20261028T074333745856Z"
	tsChild := "20261028T074333695856Z"
	childDotted := uibroDottedPart(tsRoot, root.String()) + "." + uibroDottedPart(tsChild, child.String())
	rootDotted := uibroDottedPart(tsRoot, root.String())

	if want := "20261028T074333745856Zd23b9ff8.20261028T074333695856Z093bdce7"; childDotted != want {
		t.Fatalf("fixture dotted_order=%q want %q", childDotted, want)
	}

	var s *Store // nil: parent not in DB yet (OOO)
	// Client often stamps trace_id=id when omitted on create_run.
	childMerged := map[string]any{
		"id": child.String(), "trace_id": child.String(), "parent_run_id": root.String(),
		"dotted_order": childDotted, "name": "early_child_llm", "run_type": "llm",
	}
	got := s.resolveTraceID(context.Background(), child, childMerged)
	if got == nil || *got != root {
		t.Fatalf("child trace_id=%v want root %s", got, root)
	}

	// Missing trace_id (strict create_run omit) also derives via short-hex+parent.
	childMerged2 := map[string]any{
		"id": child.String(), "parent_run_id": root.String(),
		"dotted_order": childDotted, "name": "early_child_llm", "run_type": "llm",
	}
	got2 := s.resolveTraceID(context.Background(), child, childMerged2)
	if got2 == nil || *got2 != root {
		t.Fatalf("omit-trace child=%v want %s", got2, root)
	}

	rootMerged := map[string]any{
		"id": root.String(), "trace_id": root.String(),
		"dotted_order": rootDotted, "name": "oo_parent_chain", "run_type": "chain",
	}
	gotRoot := s.resolveTraceID(context.Background(), root, rootMerged)
	if gotRoot == nil || *gotRoot != root {
		t.Fatalf("root=%v", gotRoot)
	}

	// Waterfall grouping + run_count == 2 under the parent trace.
	runs := []map[string]any{
		{
			"id": child.String(), "trace_id": got.String(), "parent_run_id": root.String(),
			"name": "early_child_llm", "run_type": "llm", "dotted_order": childDotted,
			"start_time": "2026-10-28T07:43:33.695856Z",
			"end_time":   "2026-10-28T07:43:33.715856Z",
		},
		{
			"id": root.String(), "trace_id": root.String(),
			"name": "oo_parent_chain", "run_type": "chain", "dotted_order": rootDotted,
			"start_time": "2026-10-28T07:43:33.745856Z",
			"end_time":   "2026-10-28T07:43:33.755856Z",
		},
	}
	detail := traces.TraceDetail(root.String(), runs)
	rows, _ := detail["rows"].([]map[string]any)
	if len(rows) != 2 {
		t.Fatalf("rows=%d detail=%v", len(rows), detail)
	}
	var rootDepth, childDepth = -1, -1
	for _, r := range rows {
		switch r["id"] {
		case root.String():
			rootDepth = r["depth"].(int)
		case child.String():
			childDepth = r["depth"].(int)
		}
	}
	if rootDepth != 0 || childDepth != 1 {
		t.Fatalf("depths root=%d child=%d", rootDepth, childDepth)
	}
	sum, _ := detail["summary"].(map[string]any)
	if n, _ := sum["run_count"].(int); n != 2 {
		t.Fatalf("run_count=%v want 2", sum["run_count"])
	}
}

func TestResolveTraceIDUndashedFullUUID(t *testing.T) {
	root := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	child := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	ts := "20260115T120304056789Z"
	undashedRoot := strings.ReplaceAll(root.String(), "-", "")
	undashedChild := strings.ReplaceAll(child.String(), "-", "")
	dotted := ts + undashedRoot + "." + "20260115T120304100000Z" + undashedChild
	merged := map[string]any{
		"id": child.String(), "trace_id": child.String(), "parent_run_id": root.String(),
		"dotted_order": dotted,
	}
	var s *Store
	got := s.resolveTraceID(context.Background(), child, merged)
	if got == nil || *got != root {
		t.Fatalf("got %v want %s", got, root)
	}
}

func TestSuffixPrefixesUUIDRejectsShortAndNonHex(t *testing.T) {
	id := uuid.MustParse("d23b9ff8-0793-43c5-8542-4e71baed406a")
	if !suffixPrefixesUUID("d23b9ff8", id) {
		t.Fatal("8-hex prefix should match")
	}
	if !suffixPrefixesUUID("D23B9FF8", id) {
		t.Fatal("case-insensitive")
	}
	if suffixPrefixesUUID("d23b9ff", id) { // 7 chars
		t.Fatal("need >= 8")
	}
	if suffixPrefixesUUID("zzzzzzzz", id) {
		t.Fatal("non-hex")
	}
	if suffixPrefixesUUID("093bdce7", id) {
		t.Fatal("other uuid prefix must not match")
	}
}

func TestResolveTraceIDFromRecordedUIBroChildPayload(t *testing.T) {
	// Exact stored child from at-m3 (d507498) after UIBro ingest_m3_final.py —
	// see testdata/uibro_m3_ingest/sample_oo_child_run.json.
	raw, err := os.ReadFile("testdata/uibro_m3_ingest/sample_oo_child_run.json")
	if err != nil {
		t.Fatal(err)
	}
	var recorded map[string]any
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatal(err)
	}
	id := asUUID(recorded["id"])
	parent := asUUID(recorded["parent_run_id"])
	if id == nil || parent == nil {
		t.Fatalf("bad fixture ids: %v", recorded)
	}
	// Reproduce the buggy stored shape: trace_id == id, short-hex dotted_order.
	if asString(recorded["trace_id"]) != id.String() {
		t.Fatalf("fixture expected orphaned trace_id==id, got %v", recorded["trace_id"])
	}
	wantDotted := "20261028T074333745856Zd23b9ff8.20261028T074333695856Z093bdce7"
	if asString(recorded["dotted_order"]) != wantDotted {
		t.Fatalf("dotted_order=%q", recorded["dotted_order"])
	}

	var s *Store
	got := s.resolveTraceID(context.Background(), *id, recorded)
	if got == nil || *got != *parent {
		t.Fatalf("resolved=%v want parent/root %s", got, parent)
	}
}
