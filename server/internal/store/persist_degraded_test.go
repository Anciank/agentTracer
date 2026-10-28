package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Anciank/agentTracer/server/internal/ingest"
)

func TestPersistBatchDegradedRetryKeepsEndTime(t *testing.T) {
	pool := testPool(t)
	st := New(pool, NewMemoryBlob())
	st.ResetIngestStatsForTest()
	ctx := context.Background()

	runID := uuid.NewString()
	traceID := runID
	start := time.Now().UTC().Add(-2 * time.Second).Format(time.RFC3339Nano)
	end := time.Now().UTC().Format(time.RFC3339Nano)

	// Create cleanly first.
	if err := st.UpsertRun(ctx, map[string]any{
		"id": runID, "trace_id": traceID, "name": "chat", "run_type": "chain",
		"session_name": "degraded-retry", "start_time": start,
	}, "post"); err != nil {
		t.Fatal(err)
	}

	// Poison: UpsertRun fails whenever outputs is non-nil.
	UpsertRunTestHook = func(payload map[string]any, op string) error {
		if v, ok := payload["outputs"]; ok && v != nil {
			return fmt.Errorf("injected poison outputs")
		}
		return nil
	}
	t.Cleanup(func() { UpsertRunTestHook = nil })

	okID := uuid.NewString()
	batch := &ingest.Batch{
		Updates: map[string]map[string]any{
			runID: {
				"id": runID, "trace_id": traceID, "end_time": end,
				"outputs": map[string]any{"messages": []any{map[string]any{"content": "毒\x00药", "type": "ai"}}},
			},
			okID: {
				"id": okID, "trace_id": okID, "name": "ok", "run_type": "chain",
				"session_name": "degraded-retry",
				"start_time":   start, "end_time": end,
			},
		},
	}
	// okID is update-only without create — UpsertRun still inserts.
	res := st.PersistBatch(ctx, batch)
	written, _ := res["written"].(int)
	if written < 1 {
		t.Fatalf("written=%v errors=%v", res["written"], res["errors"])
	}

	stats := st.IngestStats()
	if stats["persist_errors"].(uint64) < 1 {
		t.Fatalf("persist_errors=%v", stats)
	}
	if stats["degraded_retries_ok"].(uint64) < 1 {
		t.Fatalf("degraded_retries_ok=%v", stats)
	}

	got, err := st.GetRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if got["end_time"] == nil || got["end_time"] == "" {
		t.Fatalf("end_time not set after degraded retry: %#v", got)
	}
	// Sibling must still persist.
	ok, err := st.GetRun(ctx, okID)
	if err != nil || ok["end_time"] == nil {
		t.Fatalf("sibling missing: err=%v got=%v", err, ok)
	}
}

func TestUpsertRunStripsNULInInputsOutputs(t *testing.T) {
	pool := testPool(t)
	st := New(pool, NewMemoryBlob())
	ctx := context.Background()
	runID := uuid.NewString()
	start := time.Now().UTC().Format(time.RFC3339Nano)
	end := time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano)
	if err := st.UpsertRun(ctx, map[string]any{
		"id": runID, "trace_id": runID, "name": "nul", "run_type": "chain",
		"session_name": "nul-strip", "start_time": start, "end_time": end,
		"inputs":  map[string]any{"q": "hello\x00world"},
		"outputs": map[string]any{"a": "ok\x00"},
		"error":   "boom\x00",
	}, "post"); err != nil {
		t.Fatalf("NUL should not fail upsert: %v", err)
	}
	got, err := st.GetRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	in, _ := got["inputs"].(map[string]any)
	if in["q"] != "helloworld" {
		t.Fatalf("inputs not stripped: %#v", in)
	}
	out, _ := got["outputs"].(map[string]any)
	if out["a"] != "ok" {
		t.Fatalf("outputs not stripped: %#v", out)
	}
	if got["error"] != "boom" {
		t.Fatalf("error not stripped: %#v", got["error"])
	}
}
