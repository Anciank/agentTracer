package traces

import "testing"

func TestTraceDetailWaterfallShape(t *testing.T) {
	parent := "11111111-1111-1111-1111-111111111111"
	child := "22222222-2222-2222-2222-222222222222"
	runs := []map[string]any{
		{"id": parent, "trace_id": parent, "name": "root", "run_type": "chain",
			"start_time": "2026-01-01T00:00:00Z", "end_time": "2026-01-01T00:00:02Z",
			"dotted_order": "20260101T000000000000Z" + parent[:8]},
		{"id": child, "trace_id": parent, "parent_run_id": parent, "name": "llm", "run_type": "llm",
			"start_time": "2026-01-01T00:00:00.500Z", "end_time": "2026-01-01T00:00:01.500Z",
			"dotted_order": "20260101T000000000000Z" + parent[:8] + ".20260101T000000500000Z" + child[:8]},
	}
	d := TraceDetail(parent, runs)
	if d == nil {
		t.Fatal("nil detail")
	}
	if d["span"].(float64) < 1.9 {
		t.Fatalf("span=%v", d["span"])
	}
	rows := d["rows"].([]map[string]any)
	if len(rows) != 2 {
		t.Fatalf("rows=%d", len(rows))
	}
	if rows[1]["depth"].(int) != 1 {
		t.Fatalf("depth=%v", rows[1]["depth"])
	}
	sums := TraceSummaries(runs)
	if len(sums) != 1 || sums[0]["run_count"].(int) != 2 {
		t.Fatalf("%v", sums)
	}
}
