package store

import (
	"context"
	"testing"
	"time"

	"github.com/Anciank/agentTracer/server/internal/dbtest"
	"github.com/Anciank/agentTracer/server/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := dbtest.TestDatabaseURL(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrations.Up(ctx, pool); err != nil {
		t.Fatal(err)
	}
	_, _ = pool.Exec(ctx, `TRUNCATE attachments, feedback, runs, sessions CASCADE`)
	return pool
}

func TestLangGraphIngestMetadataFallbackAndPatch(t *testing.T) {
	pool := testPool(t)
	st := New(pool, NewMemoryBlob())
	ctx := context.Background()

	rootID := uuid.NewString()
	childID := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Root named LangGraph (heuristic)
	if err := st.UpsertRun(ctx, map[string]any{
		"id": rootID, "trace_id": rootID, "name": "LangGraph", "run_type": "chain",
		"session_name": "lg-proj", "start_time": now,
	}, "post"); err != nil {
		t.Fatal(err)
	}

	// Child without metadata → name fallback
	if err := st.UpsertRun(ctx, map[string]any{
		"id": childID, "trace_id": rootID, "parent_run_id": rootID,
		"name": "search", "run_type": "tool", "session_name": "lg-proj", "start_time": now,
	}, "post"); err != nil {
		t.Fatal(err)
	}
	child, err := st.GetRun(ctx, childID)
	if err != nil {
		t.Fatal(err)
	}
	if asString(child["langgraph_node"]) != "search" {
		t.Fatalf("fallback want search got %v", child["langgraph_node"])
	}

	// PATCH later with real metadata + step
	if err := st.UpsertRun(ctx, map[string]any{
		"id": childID,
		"extra": map[string]any{
			"metadata": map[string]any{
				"langgraph_node": "search_v2",
				"langgraph_step": 2,
			},
		},
		"end_time": time.Now().UTC().Format(time.RFC3339Nano),
	}, "patch"); err != nil {
		t.Fatal(err)
	}
	child, err = st.GetRun(ctx, childID)
	if err != nil {
		t.Fatal(err)
	}
	if asString(child["langgraph_node"]) != "search_v2" {
		t.Fatalf("patch node=%v", child["langgraph_node"])
	}
	if asInt(child["langgraph_step"]) != 2 {
		t.Fatalf("patch step=%v", child["langgraph_step"])
	}

	// Direct metadata on create
	other := uuid.NewString()
	if err := st.UpsertRun(ctx, map[string]any{
		"id": other, "trace_id": other, "name": "agent", "run_type": "chain",
		"session_name": "lg-proj", "start_time": now,
		"extra": map[string]any{"metadata": map[string]any{
			"langgraph_node": "agent", "langgraph_step": 1,
		}},
	}, "post"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetRun(ctx, other)
	if asString(got["langgraph_node"]) != "agent" || asInt(got["langgraph_step"]) != 1 {
		t.Fatalf("direct meta: %+v", got)
	}
}
