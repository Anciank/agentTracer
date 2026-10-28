package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBackfillChatPreviewsIdempotentAndCold(t *testing.T) {
	pool := testPool(t)
	blob := NewMemoryBlob()
	st := New(pool, blob)
	ctx := context.Background()
	proj := "cb2-preview-backfill"
	now := time.Now().UTC()

	// Trace A: inline payloads, empty preview columns (simulates pre-column ingest after ALTER DEFAULT '').
	rootA := uuid.New()
	childA := uuid.New()
	mustUpsert := func(p map[string]any) {
		t.Helper()
		if err := st.UpsertRun(ctx, p, "post"); err != nil {
			t.Fatal(err)
		}
	}
	mustUpsert(map[string]any{
		"id": rootA.String(), "trace_id": rootA.String(), "name": "agent", "run_type": "chain",
		"session_name": proj, "start_time": now.Add(-2 * time.Minute).Format(time.RFC3339Nano),
		"end_time": now.Add(-time.Minute).Format(time.RFC3339Nano), "status": "success",
		"inputs": map[string]any{"messages": []any{
			map[string]any{"role": "user", "content": "backfill user hello"},
		}},
	})
	mustUpsert(map[string]any{
		"id": childA.String(), "trace_id": rootA.String(), "parent_run_id": rootA.String(),
		"name": "llm", "run_type": "llm", "session_name": proj,
		"start_time": now.Add(-90 * time.Second).Format(time.RFC3339Nano),
		"end_time":   now.Add(-80 * time.Second).Format(time.RFC3339Nano), "status": "success",
		"outputs": map[string]any{"messages": []any{
			map[string]any{"role": "assistant", "content": "backfill ai reply"},
		}},
	})
	// Clear denorm columns that UpsertRun just filled — simulate legacy rows.
	if _, err := pool.Exec(ctx, `
		UPDATE runs SET first_user_preview='', last_ai_preview='' WHERE trace_id=$1`, rootA); err != nil {
		t.Fatal(err)
	}

	// Trace B: cold _s3 pointer for inputs on root.
	rootB := uuid.New()
	inPayload, _ := json.Marshal(map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "cold user from s3"}},
	})
	key := "runs/" + rootB.String() + "/inputs.json"
	if err := blob.Put(ctx, key, "application/json", inPayload); err != nil {
		t.Fatal(err)
	}
	outPayload, _ := json.Marshal(map[string]any{
		"messages": []any{map[string]any{"role": "assistant", "content": "cold ai from s3"}},
	})
	outKey := "runs/" + rootB.String() + "/outputs.json"
	if err := blob.Put(ctx, key, "application/json", inPayload); err != nil {
		t.Fatal(err)
	}
	_ = blob.Put(ctx, outKey, "application/json", outPayload)
	mustUpsert(map[string]any{
		"id": rootB.String(), "trace_id": rootB.String(), "name": "agent", "run_type": "chain",
		"session_name": proj, "start_time": now.Add(-5 * time.Minute).Format(time.RFC3339Nano),
		"end_time": now.Add(-4 * time.Minute).Format(time.RFC3339Nano), "status": "success",
		"inputs":  map[string]any{"_s3": key},
		"outputs": map[string]any{"_s3": outKey},
	})
	if _, err := pool.Exec(ctx, `
		UPDATE runs SET first_user_preview='', last_ai_preview='' WHERE trace_id=$1`, rootB); err != nil {
		t.Fatal(err)
	}

	wrote, failed, scanned, err := st.BackfillChatPreviews(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if failed != 0 {
		t.Fatalf("failed=%d", failed)
	}
	if wrote < 2 || scanned < 2 {
		t.Fatalf("wrote=%d scanned=%d want >=2", wrote, scanned)
	}

	var fu, la string
	if err := pool.QueryRow(ctx, `
		SELECT first_user_preview, last_ai_preview FROM runs
		WHERE id=$1`, rootA).Scan(&fu, &la); err != nil {
		t.Fatal(err)
	}
	if fu != "backfill user hello" || la != "backfill ai reply" {
		t.Fatalf("trace A got fu=%q la=%q", fu, la)
	}
	if err := pool.QueryRow(ctx, `
		SELECT first_user_preview, last_ai_preview FROM runs
		WHERE id=$1`, rootB).Scan(&fu, &la); err != nil {
		t.Fatal(err)
	}
	if fu != "cold user from s3" || la != "cold ai from s3" {
		t.Fatalf("trace B cold got fu=%q la=%q", fu, la)
	}

	// Second run: idempotent noop (no empty roots left for these traces).
	wrote2, failed2, _, err := st.BackfillChatPreviews(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if failed2 != 0 {
		t.Fatalf("failed2=%d", failed2)
	}
	// May still scan other leftover roots from shared test DB; but these two must not re-write.
	_ = wrote2
	if err := pool.QueryRow(ctx, `
		SELECT first_user_preview, last_ai_preview FROM runs WHERE id=$1`, rootA).Scan(&fu, &la); err != nil {
		t.Fatal(err)
	}
	if fu != "backfill user hello" || la != "backfill ai reply" {
		t.Fatalf("idempotent overwrite? fu=%q la=%q", fu, la)
	}
}

func TestResolveMaybeCold(t *testing.T) {
	blob := NewMemoryBlob()
	st := New(nil, blob)
	ctx := context.Background()
	raw := []byte(`{"messages":[{"role":"user","content":"x"}]}`)
	_ = blob.Put(ctx, "k1", "application/json", raw)
	got := st.resolveMaybeCold(ctx, map[string]any{"_s3": "k1"})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("got %T", got)
	}
	msgs, _ := m["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("msgs=%v", msgs)
	}
	// non-cold passthrough
	passthrough := map[string]any{"a": 1}
	got2 := st.resolveMaybeCold(ctx, passthrough)
	if m2, ok := got2.(map[string]any); !ok || m2["a"] != 1 {
		t.Fatalf("passthrough=%v", got2)
	}
}
