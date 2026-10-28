package migrations

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Anciank/agentTracer/server/internal/dbtest"
)

func testDSN(t *testing.T) string {
	t.Helper()
	return dbtest.TestDatabaseURL(t)
}

func resetSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
}

func applyRawSQL(t *testing.T, pool *pgxpool.Pool, names ...string) {
	t.Helper()
	ctx := context.Background()
	for _, name := range names {
		body, err := FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("raw %s: %v", name, err)
		}
	}
}

func indexExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname='public' AND indexname=$1)`, name,
	).Scan(&exists)
	if err != nil {
		t.Fatal(err)
	}
	return exists
}

func tableExistsPublic(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(),
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name=$1)`, name,
	).Scan(&exists)
	if err != nil {
		t.Fatal(err)
	}
	return exists
}

var wantAll = []string{"001_init", "002_run_metrics", "003_langgraph", "004_agent_map", "005_trace_graph_cache", "006_graph_cache_lat_sentinel", "007_monitor_indexes", "008_monitor_covering_indexes", "009_trace_failed_index"}

func assertVersions(t *testing.T, versions, want []string) {
	t.Helper()
	if len(versions) < len(want) {
		t.Fatalf("versions=%v want %v", versions, want)
	}
	for i, w := range want {
		if versions[i] != w {
			t.Fatalf("versions[%d]=%v want %v (all=%v)", i, versions[i], w, versions)
		}
	}
}

func TestUpFreshAndIdempotent(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	resetSchema(t, pool)
	if err := Up(ctx, pool); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	versions, err := AppliedVersions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	assertVersions(t, versions, wantAll)
	if !tableExistsPublic(t, pool, "trace_graph_cache") {
		t.Fatal("005 did not create trace_graph_cache")
	}
	if err := Up(ctx, pool); err != nil {
		t.Fatalf("second Up: %v", err)
	}
	versions2, _ := AppliedVersions(ctx, pool)
	if len(versions2) != len(versions) {
		t.Fatalf("idempotent mismatch %v vs %v", versions, versions2)
	}
}

// Prod-shaped: 001-004 already applied as raw IF NOT EXISTS SQL, no schema_migrations.
// Bootstrap marks 001-003; 004 runs as no-op; 005 creates trace_graph_cache; rows preserved.
func TestUpBootstrapProdDumpNoOp004(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	resetSchema(t, pool)
	applyRawSQL(t, pool, "001_init.sql", "002_run_metrics.sql", "003_langgraph.sql", "004_agent_map.sql")
	_, err = pool.Exec(ctx, `INSERT INTO sessions(id, name) VALUES ('11111111-1111-1111-1111-111111111111', 'bootstrap-keep')`)
	if err != nil {
		t.Fatal(err)
	}
	if !indexExists(t, pool, "idx_runs_session_root_start") {
		t.Fatal("precondition: 004 indexes should already exist")
	}
	if tableExistsPublic(t, pool, "trace_graph_cache") {
		t.Fatal("precondition: 005 table must NOT exist yet")
	}

	if err := Up(ctx, pool); err != nil {
		t.Fatalf("bootstrap Up: %v", err)
	}
	versions, err := AppliedVersions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	assertVersions(t, versions, wantAll)
	if !tableExistsPublic(t, pool, "trace_graph_cache") {
		t.Fatal("005 did not create trace_graph_cache")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE name='bootstrap-keep'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("row wiped; keep-row count=%d", n)
	}
	if err := Up(ctx, pool); err != nil {
		t.Fatal(err)
	}
}

// Older DB stopped at 003: bootstrap marks 001-003; 004 creates indexes; 005 creates cache table.
func TestUpBootstrapFrom003ThenRuns004(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	resetSchema(t, pool)
	applyRawSQL(t, pool, "001_init.sql", "002_run_metrics.sql", "003_langgraph.sql")
	if indexExists(t, pool, "idx_runs_session_root_start") {
		t.Fatal("precondition: 004 index must NOT exist yet")
	}

	if err := Up(ctx, pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	versions, err := AppliedVersions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	assertVersions(t, versions, wantAll)
	for _, idx := range []string{
		"idx_runs_session_root_start",
		"idx_runs_session_fingerprint",
		"idx_runs_trace_visit",
		"idx_runs_session_start_trace",
	} {
		if !indexExists(t, pool, idx) {
			t.Fatalf("004 did not create index %s", idx)
		}
	}
	if !tableExistsPublic(t, pool, "trace_graph_cache") {
		t.Fatal("005 did not create trace_graph_cache")
	}
}

func TestListSQLIncludes005Through009(t *testing.T) {
	files, err := listSQL()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"005_trace_graph_cache.sql":        false,
		"006_graph_cache_lat_sentinel.sql": false,
		"007_monitor_indexes.sql":          false,
		"008_monitor_covering_indexes.sql": false,
		"009_trace_failed_index.sql":       false,
	}
	for _, f := range files {
		if _, ok := want[f]; ok {
			want[f] = true
		}
	}
	for f, ok := range want {
		if !ok {
			t.Fatalf("%s missing from listSQL: %v", f, files)
		}
	}
}
