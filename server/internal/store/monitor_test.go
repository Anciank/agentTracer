package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMonitorBucketSpecs(t *testing.T) {
	s, err := monitorBucketForWindow("1h")
	if err != nil || s.Points != 60 || s.Dur != time.Minute || s.Bucket != "1m" {
		t.Fatalf("%+v %v", s, err)
	}
	s, err = monitorBucketForWindow("24h")
	if err != nil || s.Points != 24 || s.Dur != time.Hour || s.Bucket != "1h" {
		t.Fatalf("%+v %v", s, err)
	}
	s, err = monitorBucketForWindow("7d")
	if err != nil || s.Points != 28 || s.Dur != 6*time.Hour || s.Bucket != "6h" {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := monitorBucketForWindow("2d"); err == nil {
		t.Fatal("expected error")
	}
}

func TestAlignBucketFloor(t *testing.T) {
	ts := time.Date(2026, 10, 28, 8, 47, 33, 123, time.UTC)
	if g := alignBucketFloor(ts, time.Minute); !g.Equal(time.Date(2026, 10, 28, 8, 47, 0, 0, time.UTC)) {
		t.Fatalf("minute %v", g)
	}
	if g := alignBucketFloor(ts, time.Hour); !g.Equal(time.Date(2026, 10, 28, 8, 0, 0, 0, time.UTC)) {
		t.Fatalf("hour %v", g)
	}
	if g := alignBucketFloor(ts, 6*time.Hour); !g.Equal(time.Date(2026, 10, 28, 6, 0, 0, 0, time.UTC)) {
		t.Fatalf("6h %v", g)
	}
	ts2 := time.Date(2026, 10, 28, 0, 15, 0, 0, time.UTC)
	if g := alignBucketFloor(ts2, 6*time.Hour); !g.Equal(time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("6h midnight %v", g)
	}
}

func TestMonitorWindowBoundsPoints(t *testing.T) {
	spec, _ := monitorBucketForWindow("7d")
	from, to, err := monitorWindowBounds(spec, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	starts := bucketStarts(from, to, spec.Dur)
	if len(starts) != 28 {
		t.Fatalf("want 28 buckets, got %d (%v .. %v)", len(starts), from, to)
	}
	spec, _ = monitorBucketForWindow("1h")
	from, to, err = monitorWindowBounds(spec, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(bucketStarts(from, to, spec.Dur)); n != 60 {
		t.Fatalf("1h want 60 got %d", n)
	}
	spec, _ = monitorBucketForWindow("24h")
	from, to, err = monitorWindowBounds(spec, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(bucketStarts(from, to, spec.Dur)); n != 24 {
		t.Fatalf("24h want 24 got %d", n)
	}
}

func TestMonitorETagStable(t *testing.T) {
	from := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	a := monitorETag("pid", "24h", from, to, 10, 2, from.Format(time.RFC3339Nano))
	b := monitorETag("pid", "24h", from, to, 10, 2, from.Format(time.RFC3339Nano))
	c := monitorETag("pid", "24h", from, to, 11, 2, from.Format(time.RFC3339Nano))
	if a != b || a == "" || a[0] != '"' {
		t.Fatalf("etag %q %q", a, b)
	}
	if a == c {
		t.Fatal("etag should change with trace_count")
	}
}


func TestUIMonitorAndToolFilter(t *testing.T) {
	pool := testPool(t)
	st := New(pool, NewMemoryBlob())
	ctx := context.Background()
	proj := "m36-monitor-test"
	now := time.Now().UTC()

	root1 := uuid.New()
	root2 := uuid.New()
	llm1 := uuid.New()
	llm2 := uuid.New()
	tool1 := uuid.New()
	tool2 := uuid.New()

	must := func(payload map[string]any, op string) {
		t.Helper()
		if err := st.UpsertRun(ctx, payload, op); err != nil {
			t.Fatalf("upsert %v: %v", payload["name"], err)
		}
	}

	end1 := now.Add(-30 * time.Minute)
	start1 := now.Add(-35 * time.Minute)
	end2 := now.Add(-10 * time.Minute)
	start2 := now.Add(-12 * time.Minute)

	must(map[string]any{
		"id": root1.String(), "trace_id": root1.String(), "name": "trace-ok", "run_type": "chain",
		"session_name": proj, "start_time": start1.Format(time.RFC3339Nano),
	}, "post")
	must(map[string]any{
		"id": llm1.String(), "trace_id": root1.String(), "parent_run_id": root1.String(),
		"name": "llm", "run_type": "llm", "model": "gpt-4o-mini", "session_name": proj,
		"start_time": start1.Add(time.Second).Format(time.RFC3339Nano),
		"end_time": end1.Format(time.RFC3339Nano),
		"input_tokens": 100, "output_tokens": 50, "total_tokens": 150,
	}, "post")
	must(map[string]any{
		"id": tool1.String(), "trace_id": root1.String(), "parent_run_id": root1.String(),
		"name": "web_search", "run_type": "tool", "session_name": proj,
		"start_time": start1.Add(2 * time.Second).Format(time.RFC3339Nano),
		"end_time": end1.Format(time.RFC3339Nano),
	}, "post")
	must(map[string]any{
		"id": root1.String(), "end_time": end1.Format(time.RFC3339Nano), "status": "success",
	}, "patch")

	must(map[string]any{
		"id": root2.String(), "trace_id": root2.String(), "name": "trace-err", "run_type": "chain",
		"session_name": proj, "start_time": start2.Format(time.RFC3339Nano),
	}, "post")
	must(map[string]any{
		"id": llm2.String(), "trace_id": root2.String(), "parent_run_id": root2.String(),
		"name": "llm", "run_type": "llm", "model": "claude-3-haiku", "session_name": proj,
		"start_time": start2.Add(time.Second).Format(time.RFC3339Nano),
		"end_time": end2.Format(time.RFC3339Nano),
		"input_tokens": 200, "output_tokens": 80, "total_tokens": 280,
	}, "post")
	must(map[string]any{
		"id": tool2.String(), "trace_id": root2.String(), "parent_run_id": root2.String(),
		"name": "issue_refund", "run_type": "tool", "session_name": proj,
		"start_time": start2.Add(2 * time.Second).Format(time.RFC3339Nano),
		"end_time": end2.Format(time.RFC3339Nano),
		"error": "boom", "status": "error",
	}, "post")
	must(map[string]any{
		"id": root2.String(), "end_time": end2.Format(time.RFC3339Nano),
		"error": "boom", "status": "error",
	}, "patch")

	out, etag, err := st.UIMonitor(ctx, MonitorOpts{Project: proj, Window: "24h"})
	if err != nil {
		t.Fatal(err)
	}
	if etag == "" || etag[0] != '"' {
		t.Fatalf("bad etag %q", etag)
	}
	if out["cost"] != nil {
		t.Fatalf("cost want null got %v", out["cost"])
	}
	if out["bucket"] != "1h" {
		t.Fatalf("bucket %v", out["bucket"])
	}
	if out["trace_count"] != 2 {
		t.Fatalf("trace_count %v", out["trace_count"])
	}
	if out["error_count"] != 1 {
		t.Fatalf("error_count %v", out["error_count"])
	}
	stSeries, ok := out["series_traces"].([]map[string]any)
	if !ok || len(stSeries) != 24 {
		t.Fatalf("series_traces %T len %v", out["series_traces"], len(stSeries))
	}
	sum := 0
	empty := 0
	for _, b := range stSeries {
		total := b["total"].(int)
		sum += total
		if total == 0 {
			empty++
			if b["success"].(int) != 0 || b["error"].(int) != 0 {
				t.Fatalf("zero-fill bucket not zero: %v", b)
			}
		}
	}
	if sum != 2 {
		t.Fatalf("series total sum %d", sum)
	}
	if empty != 22 {
		t.Fatalf("want 22 zero-filled buckets, got %d", empty)
	}
	models, _ := out["by_model"].([]map[string]any)
	if len(models) < 2 {
		t.Fatalf("by_model %v", models)
	}
	tools, _ := out["by_tool"].([]map[string]any)
	if len(tools) < 2 {
		t.Fatalf("by_tool %v", tools)
	}
	foundRefund := false
	for _, tr := range tools {
		if tr["tool"] == "issue_refund" {
			foundRefund = true
			if tr["tokens"] != nil {
				t.Fatalf("tool tokens want null got %v", tr["tokens"])
			}
		}
	}
	if !foundRefund {
		t.Fatal("missing issue_refund tool")
	}
	toks, _ := out["tokens"].(map[string]any)
	if asInt(toks["total"]) != 430 {
		t.Fatalf("header tokens %v", toks)
	}

	list, _, err := st.UIListTraces(ctx, TraceListOpts{Project: proj, Tool: "web_search", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0]["trace_id"] != root1.String() {
		t.Fatalf("tool filter web_search: %v", list)
	}
	list, _, err = st.UIListTraces(ctx, TraceListOpts{Project: proj, Tool: "issue_refund", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0]["trace_id"] != root2.String() {
		t.Fatalf("tool filter refund: %v", list)
	}
	list, _, err = st.UIListTraces(ctx, TraceListOpts{Project: proj, Model: "gpt-4o-mini", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("model filter: %v", list)
	}

	out7, _, err := st.UIMonitor(ctx, MonitorOpts{Project: proj, Window: "7d"})
	if err != nil {
		t.Fatal(err)
	}
	if out7["bucket"] != "6h" {
		t.Fatalf("7d bucket %v", out7["bucket"])
	}
	s7, _ := out7["series_traces"].([]map[string]any)
	if len(s7) != 28 {
		t.Fatalf("7d series %d", len(s7))
	}
}
