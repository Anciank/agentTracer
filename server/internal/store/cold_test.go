package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func seedFinishedTrace(t *testing.T, st *Store, name, errMsg string, age time.Duration, big bool) (traceID, rootID string) {
	t.Helper()
	ctx := context.Background()
	traceID = uuid.New().String()
	rootID = uuid.New().String()
	childID := uuid.New().String()
	start := time.Now().UTC().Add(-age - time.Hour)
	end := time.Now().UTC().Add(-age)
	payload := map[string]any{
		"id": rootID, "trace_id": traceID, "name": name, "run_type": "chain",
		"start_time": start.Format(time.RFC3339Nano),
		"end_time":   end.Format(time.RFC3339Nano),
		"status":     "error",
		"error":      errMsg,
		"tags":       []any{"cold-test", "m4"},
		"inputs":     map[string]any{"prompt": "hello cold"},
		"outputs":    map[string]any{"messages": []any{map[string]any{"role": "assistant", "content": "bye"}}},
		"extra":      map[string]any{"langgraph_node": "agent", "langgraph_step": 1},
		"session_name": "m4-cold",
	}
	if big {
		payload["outputs"] = map[string]any{"blob": strings.Repeat("X", 2<<20)} // ~2MiB
	}
	if err := st.UpsertRun(ctx, payload, "post"); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	if err := st.UpsertRun(ctx, map[string]any{
		"id": childID, "trace_id": traceID, "parent_run_id": rootID,
		"name": "llm", "run_type": "llm",
		"start_time": start.Add(time.Second).Format(time.RFC3339Nano),
		"end_time":   end.Format(time.RFC3339Nano),
		"status":     "success",
		"inputs":     map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}}},
		"outputs":    map[string]any{"messages": []any{map[string]any{"role": "assistant", "content": "yo"}}},
		"model":      "test-model",
		"total_tokens": 42,
	}, "post"); err != nil {
		t.Fatalf("seed child: %v", err)
	}
	return traceID, rootID
}

func TestColdArchiveHydrateRehot(t *testing.T) {
	pool := testPool(t)
	st := New(pool, NewMemoryBlob())
	st.ResetColdStatsForTest()
	ctx := context.Background()

	traceID, rootID := seedFinishedTrace(t, st, "cold-root", "boom-unique-err", 48*time.Hour, false)

	n, err := st.ArchiveBatch(ctx, ColdArchiveOpts{OlderThan: 24 * time.Hour, IngestGuard: 0, Limit: 10})
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if n < 1 {
		t.Fatalf("expected archive, got %d", n)
	}
	cold, err := st.IsTraceCold(ctx, traceID)
	if err != nil || !cold {
		t.Fatalf("expected cold: cold=%v err=%v", cold, err)
	}
	nullOK, err := st.HeavyColsNullForTest(ctx, traceID)
	if err != nil || !nullOK {
		t.Fatalf("heavy cols should be NULL: ok=%v err=%v", nullOK, err)
	}

	// Hot index: full error still present without hydrate.
	var errStr string
	if err := pool.QueryRow(ctx, `SELECT error FROM runs WHERE id=$1`, uuid.MustParse(rootID)).Scan(&errStr); err != nil {
		t.Fatal(err)
	}
	if errStr != "boom-unique-err" {
		t.Fatalf("error not hot: %q", errStr)
	}

	// Hydrate restores inputs/outputs
	run, err := st.GetRun(ctx, rootID)
	if err != nil {
		t.Fatal(err)
	}
	in, _ := run["inputs"].(map[string]any)
	if in == nil || in["prompt"] != "hello cold" {
		t.Fatalf("hydrate inputs: %#v", run["inputs"])
	}
	stats := st.ColdStats()
	if stats["hydrated"].(uint64) < 1 {
		t.Fatalf("hydrated counter: %#v", stats)
	}

	// Late PATCH re-hots
	if err := st.UpsertRun(ctx, map[string]any{
		"id": rootID, "outputs": map[string]any{"messages": []any{"patched"}},
	}, "patch"); err != nil {
		t.Fatalf("late patch: %v", err)
	}
	cold, _ = st.IsTraceCold(ctx, traceID)
	if cold {
		t.Fatal("expected rehot after late patch")
	}
	nullOK, _ = st.HeavyColsNullForTest(ctx, traceID)
	if nullOK {
		t.Fatal("heavy cols should be restored after rehot")
	}
	run, _ = st.GetRun(ctx, rootID)
	out, _ := run["outputs"].(map[string]any)
	if out == nil {
		t.Fatal("outputs missing after rehot+patch")
	}
}

func TestColdSkipsOpenAndFresh(t *testing.T) {
	pool := testPool(t)
	st := New(pool, NewMemoryBlob())
	ctx := context.Background()

	traceID := uuid.New().String()
	rootID := uuid.New().String()
	start := time.Now().UTC().Add(-48 * time.Hour)
	// Open run (no end_time)
	if err := st.UpsertRun(ctx, map[string]any{
		"id": rootID, "trace_id": traceID, "name": "open", "run_type": "chain",
		"start_time": start.Format(time.RFC3339Nano), "status": "running",
		"session_name": "m4-cold",
	}, "post"); err != nil {
		t.Fatal(err)
	}
	n, err := st.ArchiveBatch(ctx, ColdArchiveOpts{OlderThan: 0, IngestGuard: 0, Limit: 10, TraceIDs: []string{traceID}})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("must not archive open run, got %d", n)
	}

	// Finish it, but keep ingest guard at 1h — freshly updated must skip.
	end := time.Now().UTC().Add(-48 * time.Hour)
	if err := st.UpsertRun(ctx, map[string]any{
		"id": rootID, "end_time": end.Format(time.RFC3339Nano), "status": "success",
		"outputs": map[string]any{"ok": true},
	}, "patch"); err != nil {
		t.Fatal(err)
	}
	n, err = st.ArchiveBatch(ctx, ColdArchiveOpts{OlderThan: 0, IngestGuard: time.Hour, Limit: 10, TraceIDs: []string{traceID}})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("must skip fresh updated_at within 1h guard, got %d", n)
	}
}

func TestColdSearchFindsHotFields(t *testing.T) {
	pool := testPool(t)
	st := New(pool, NewMemoryBlob())
	ctx := context.Background()

	traceID, _ := seedFinishedTrace(t, st, "searchable-name", "find-me-error-xyz", 72*time.Hour, false)
	n, err := st.ArchiveBatch(ctx, ColdArchiveOpts{OlderThan: 24 * time.Hour, IngestGuard: 0, Limit: 10, TraceIDs: []string{traceID}})
	if err != nil || n < 1 {
		t.Fatalf("archive n=%d err=%v", n, err)
	}

	for _, q := range []string{"searchable-name", "find-me-error-xyz", "cold-test", "hello cold"} {
		list, _, err := st.UIListTraces(ctx, TraceListOpts{Q: q, Limit: 20})
		if err != nil {
			t.Fatalf("list q=%q: %v", q, err)
		}
		found := false
		for _, item := range list {
			if item["trace_id"] == traceID {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("q=%q did not find cold trace %s (list len=%d)", q, traceID, len(list))
		}
	}
}
