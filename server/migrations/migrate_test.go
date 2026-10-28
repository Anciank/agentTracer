package migrations

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("AGENTTRACER_DATABASE_URL")
	if dsn == "" {
		t.Skip("AGENTTRACER_DATABASE_URL not set")
	}
	return dsn
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
	want := []string{"001_init", "002_run_metrics", "003_langgraph", "004_agent_map"}
	if len(versions) < len(want) {
		t.Fatalf("expected >=%d versions, got %v", len(want), versions)
	}
	for i, w := range want {
		if versions[i] != w {
			t.Fatalf("versions[%d]=%v want %v (all=%v)", i, versions[i], w, versions)
		}
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
// Bootstrap marks 001-003; 004 runs as a no-op; data rows preserved.
func TestUpBootstrapProdDumpNoOp004(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	resetSchema(t, pool)
	files, err := listSQL()
	if err != nil {
		t.Fatal(err)
	}
	// Apply every migration file raw (like today's prod before schema_migrations).
	applyRawSQL(t, pool, files...)
	_, err = pool.Exec(ctx, `INSERT INTO sessions(id, name) VALUES ('11111111-1111-1111-1111-111111111111', 'bootstrap-keep')`)
	if err != nil {
		t.Fatal(err)
	}
	if !indexExists(t, pool, "idx_runs_session_root_start") {
		t.Fatal("precondition: 004 indexes should already exist")
	}

	if err := Up(ctx, pool); err != nil {
		t.Fatalf("bootstrap Up: %v", err)
	}
	versions, err := AppliedVersions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"001_init", "002_run_metrics", "003_langgraph", "004_agent_map"}
	for i, w := range want {
		if i >= len(versions) || versions[i] != w {
			t.Fatalf("versions=%v want prefix %v", versions, want)
		}
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

// Older DB stopped at 003: no schema_migrations, no 004 indexes.
// Bootstrap marks 001-003; 004 must actually create indexes; schema_migrations ends 001-004.
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
	if indexExists(t, pool, "idx_runs_trace_visit") {
		t.Fatal("precondition: 004 index must NOT exist yet")
	}

	if err := Up(ctx, pool); err != nil {
		t.Fatalf("Up: %v", err)
	}
	versions, err := AppliedVersions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"001_init", "002_run_metrics", "003_langgraph", "004_agent_map"}
	if len(versions) < len(want) {
		t.Fatalf("versions=%v want %v", versions, want)
	}
	for i, w := range want {
		if versions[i] != w {
			t.Fatalf("versions[%d]=%v want %v (all=%v)", i, versions[i], w, versions)
		}
	}
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
}
