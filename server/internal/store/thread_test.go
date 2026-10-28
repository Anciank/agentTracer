package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestResolveThreadKeyPrecedence(t *testing.T) {
	tid, src := resolveThreadKey(map[string]any{
		"extra": map[string]any{"metadata": map[string]any{
			"thread_id":  "t-1",
			"session_id": "s-1",
		}},
	})
	if tid == nil || *tid != "t-1" || src == nil || *src != "thread_id" {
		t.Fatalf("prefer thread_id: tid=%v src=%v", tid, src)
	}

	tid, src = resolveThreadKey(map[string]any{
		"extra": map[string]any{"metadata": map[string]any{
			"session_id": "s-only",
		}},
	})
	if tid == nil || *tid != "s-only" || src == nil || *src != "session_id" {
		t.Fatalf("fallback session_id: tid=%v src=%v", tid, src)
	}

	tid, src = resolveThreadKey(map[string]any{
		"extra": map[string]any{"metadata": map[string]any{
			"conversation_id": "ignored",
		}},
	})
	if tid != nil || src != nil {
		t.Fatalf("conversation_id must be ignored: tid=%v src=%v", tid, src)
	}

	tid, src = resolveThreadKey(map[string]any{
		"extra": map[string]any{"metadata": map[string]any{
			"thread_id": "  ",
		}},
	})
	if tid != nil {
		t.Fatalf("blank thread_id should yield nil, got %v", *tid)
	}

	tid, src = resolveThreadKey(map[string]any{"session_id": "project-uuid-not-a-key"})
	if tid != nil {
		t.Fatalf("runs.session_id must not be used: %v", *tid)
	}
}

func TestUIListThreadsStatusAndHideUnthreaded(t *testing.T) {
	pool := testPool(t)
	st := New(pool, NewMemoryBlob())
	ctx := context.Background()
	proj := "cb2-threads-" + uuid.New().String()[:8]
	now := time.Now().UTC()

	mustUpsert := func(p map[string]any) {
		t.Helper()
		if err := st.UpsertRun(ctx, p, "post"); err != nil {
			t.Fatal(err)
		}
	}
	mkRoot := func(id uuid.UUID, thread any, metaKey string, start time.Time, status, errStr string, msgs ...any) {
		t.Helper()
		extra := map[string]any{"metadata": map[string]any{}}
		if thread != nil {
			extra["metadata"].(map[string]any)[metaKey] = thread
		}
		payload := map[string]any{
			"id": id.String(), "trace_id": id.String(), "name": "LangGraph", "run_type": "chain",
			"session_name": proj,
			"start_time":   start.Format(time.RFC3339Nano),
			"end_time":     start.Add(time.Second).Format(time.RFC3339Nano),
			"status":       status,
			"extra":        extra,
			"inputs": map[string]any{"messages": []any{
				map[string]any{"role": "user", "content": "user-" + id.String()[:8]},
			}},
			"outputs": map[string]any{"messages": []any{
				map[string]any{"role": "assistant", "content": "ai-" + id.String()[:8]},
			}},
		}
		if errStr != "" {
			payload["error"] = errStr
		}
		mustUpsert(payload)
	}

	// Thread A: old turn errored, latest succeeded → status success, error_count 1
	a1 := uuid.New()
	a2 := uuid.New()
	mkRoot(a1, "thread-A", "thread_id", now.Add(-10*time.Minute), "error", "boom")
	mkRoot(a2, "thread-A", "thread_id", now.Add(-1*time.Minute), "success", "")

	// Thread B: session_id only
	b1 := uuid.New()
	mkRoot(b1, "sess-B", "session_id", now.Add(-2*time.Minute), "success", "")

	// Unthreaded: no key → hidden
	u1 := uuid.New()
	mkRoot(u1, nil, "thread_id", now.Add(-30*time.Second), "success", "")

	// conversation_id only → hidden
	c1 := uuid.New()
	mustUpsert(map[string]any{
		"id": c1.String(), "trace_id": c1.String(), "name": "LangGraph", "run_type": "chain",
		"session_name": proj,
		"start_time":   now.Add(-20 * time.Second).Format(time.RFC3339Nano),
		"end_time":     now.Add(-15 * time.Second).Format(time.RFC3339Nano),
		"status":       "success",
		"extra":        map[string]any{"metadata": map[string]any{"conversation_id": "conv-ignored"}},
	})

	list, next, err := st.UIListThreads(ctx, ThreadListOpts{Project: proj, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if next != "" {
		t.Fatalf("unexpected next_cursor %q", next)
	}
	byID := map[string]map[string]any{}
	for _, item := range list {
		byID[item["thread_id"].(string)] = item
	}
	if len(byID) != 2 {
		t.Fatalf("want 2 threads (A+B), got %d: %#v", len(byID), byID)
	}
	a := byID["thread-A"]
	if a == nil {
		t.Fatal("missing thread-A")
	}
	if a["status"] != "success" {
		t.Fatalf("thread-A status=%v want success", a["status"])
	}
	if a["error_count"] != 1 {
		t.Fatalf("thread-A error_count=%v want 1", a["error_count"])
	}
	if a["turn_count"] != 2 {
		t.Fatalf("thread-A turn_count=%v want 2", a["turn_count"])
	}
	if a["thread_key_source"] != "thread_id" {
		t.Fatalf("thread-A key_source=%v", a["thread_key_source"])
	}
	b := byID["sess-B"]
	if b == nil {
		t.Fatal("missing sess-B")
	}
	if b["thread_key_source"] != "session_id" {
		t.Fatalf("sess-B key_source=%v", b["thread_key_source"])
	}
	if b["status"] != "success" || b["error_count"] != 0 || b["turn_count"] != 1 {
		t.Fatalf("sess-B unexpected: %#v", b)
	}

	// Detail: oldest first
	traces, _, err := st.UIListThreadTraces(ctx, proj, "thread-A", 50, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(traces) != 2 {
		t.Fatalf("thread-A traces=%d", len(traces))
	}
	if traces[0]["trace_id"] != a1.String() || traces[1]["trace_id"] != a2.String() {
		t.Fatalf("order wrong: %v then %v", traces[0]["trace_id"], traces[1]["trace_id"])
	}

	// Denorm columns set at ingest
	var tid, src *string
	if err := pool.QueryRow(ctx, `SELECT thread_id, thread_key_source FROM runs WHERE id=$1`, a1).Scan(&tid, &src); err != nil {
		t.Fatal(err)
	}
	if tid == nil || *tid != "thread-A" || src == nil || *src != "thread_id" {
		t.Fatalf("denorm a1: tid=%v src=%v", tid, src)
	}
	if err := pool.QueryRow(ctx, `SELECT thread_id, thread_key_source FROM runs WHERE id=$1`, b1).Scan(&tid, &src); err != nil {
		t.Fatal(err)
	}
	if tid == nil || *tid != "sess-B" || src == nil || *src != "session_id" {
		t.Fatalf("denorm b1: tid=%v src=%v", tid, src)
	}
	if err := pool.QueryRow(ctx, `SELECT thread_id FROM runs WHERE id=$1`, u1).Scan(&tid); err != nil {
		t.Fatal(err)
	}
	if tid != nil {
		t.Fatalf("unthreaded should stay null, got %v", *tid)
	}
}

func TestBackfillThreadIDsIdempotent(t *testing.T) {
	pool := testPool(t)
	st := New(pool, NewMemoryBlob())
	ctx := context.Background()
	proj := "cb2-thread-bf-" + uuid.New().String()[:8]
	now := time.Now().UTC()
	root := uuid.New()
	if err := st.UpsertRun(ctx, map[string]any{
		"id": root.String(), "trace_id": root.String(), "name": "LangGraph", "run_type": "chain",
		"session_name": proj,
		"start_time":   now.Add(-time.Minute).Format(time.RFC3339Nano),
		"end_time":     now.Format(time.RFC3339Nano),
		"status":       "success",
		"extra":        map[string]any{"metadata": map[string]any{"thread_id": "bf-thread-1"}},
	}, "post"); err != nil {
		t.Fatal(err)
	}
	// Simulate pre-change ingest: clear denorm columns.
	if _, err := pool.Exec(ctx, `UPDATE runs SET thread_id=NULL, thread_key_source=NULL WHERE id=$1`, root); err != nil {
		t.Fatal(err)
	}

	wrote, failed, scanned, err := st.BackfillThreadIDs(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if failed != 0 {
		t.Fatalf("failed=%d", failed)
	}
	if wrote < 1 || scanned < 1 {
		t.Fatalf("wrote=%d scanned=%d", wrote, scanned)
	}
	var tid, src string
	if err := pool.QueryRow(ctx, `SELECT thread_id, thread_key_source FROM runs WHERE id=$1`, root).Scan(&tid, &src); err != nil {
		t.Fatal(err)
	}
	if tid != "bf-thread-1" || src != "thread_id" {
		t.Fatalf("after backfill tid=%q src=%q", tid, src)
	}

	wrote2, failed2, _, err := st.BackfillThreadIDs(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if failed2 != 0 {
		t.Fatalf("failed2=%d", failed2)
	}
	// This root must not be rewritten (already has thread_id).
	_ = wrote2
	if err := pool.QueryRow(ctx, `SELECT thread_id, thread_key_source FROM runs WHERE id=$1`, root).Scan(&tid, &src); err != nil {
		t.Fatal(err)
	}
	if tid != "bf-thread-1" || src != "thread_id" {
		t.Fatalf("idempotent overwrite? tid=%q src=%q", tid, src)
	}
}
