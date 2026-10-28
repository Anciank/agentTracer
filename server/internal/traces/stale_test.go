package traces

import (
	"testing"
	"time"
)

func TestRunStatusStaleBoundaries(t *testing.T) {
	SetStaleAfter(time.Hour)
	defer SetStaleAfter(time.Hour)

	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	mk := func(start time.Time, end any, err any) map[string]any {
		m := map[string]any{
			"start_time": start.UTC().Format(time.RFC3339Nano),
			"end_time":   end,
		}
		if err != nil {
			m["error"] = err
		}
		return m
	}

	justUnder := now.Add(-time.Hour + time.Nanosecond)
	exact := now.Add(-time.Hour)
	justOver := now.Add(-time.Hour - time.Nanosecond)

	if got := RunStatusAt(mk(justUnder, nil, nil), now); got != "running" {
		t.Fatalf("age = threshold-1ns: want running, got %q", got)
	}
	if got := RunStatusAt(mk(exact, nil, nil), now); got != "stale" {
		t.Fatalf("age = threshold: want stale, got %q", got)
	}
	if got := RunStatusAt(mk(justOver, nil, nil), now); got != "stale" {
		t.Fatalf("age = threshold+1ns: want stale, got %q", got)
	}
	// ended run is never stale
	end := now.Add(-30 * time.Minute).UTC().Format(time.RFC3339Nano)
	if got := RunStatusAt(mk(justOver, end, nil), now); got != "success" {
		t.Fatalf("ended run: want success, got %q", got)
	}
	// error wins over stale
	if got := RunStatusAt(mk(justOver, nil, "boom"), now); got != "error" {
		t.Fatalf("error open run: want error, got %q", got)
	}
	// status=error column also counts
	r := mk(justOver, nil, nil)
	r["status"] = "error"
	if got := RunStatusAt(r, now); got != "error" {
		t.Fatalf("status=error: want error, got %q", got)
	}
}

func TestTraceSummaryStaleRollup(t *testing.T) {
	SetStaleAfter(time.Hour)
	defer SetStaleAfter(time.Hour)

	now := time.Now().UTC()
	oldStart := now.Add(-2 * time.Hour).Format(time.RFC3339Nano)
	freshStart := now.Add(-5 * time.Minute).Format(time.RFC3339Nano)
	runs := []map[string]any{
		{"id": "root", "name": "agent", "start_time": oldStart, "end_time": nil, "session_name": "p"},
		{"id": "child", "parent_run_id": "root", "name": "tool", "start_time": freshStart, "end_time": nil, "session_name": "p"},
	}
	sum := TraceSummary("t1", runs)
	// fresh child → still running (running wins over stale)
	if sum["status"] != "running" {
		t.Fatalf("want running (fresh child), got %v stale=%v running=%v", sum["status"], sum["stale_count"], sum["running_count"])
	}
	// only stale
	runs2 := []map[string]any{
		{"id": "root", "name": "agent", "start_time": oldStart, "end_time": nil, "session_name": "p"},
	}
	sum2 := TraceSummary("t2", runs2)
	if sum2["status"] != "stale" {
		t.Fatalf("want stale, got %v", sum2["status"])
	}
	if sum2["stale_count"] != 1 {
		t.Fatalf("stale_count=%v", sum2["stale_count"])
	}
}

func TestConfigStaleAfterDefaultViaSet(t *testing.T) {
	SetStaleAfter(90 * time.Minute)
	if StaleAfter() != 90*time.Minute {
		t.Fatalf("got %v", StaleAfter())
	}
	SetStaleAfter(0) // no-op
	if StaleAfter() != 90*time.Minute {
		t.Fatalf("zero should keep prior, got %v", StaleAfter())
	}
	SetStaleAfter(time.Hour)
}
