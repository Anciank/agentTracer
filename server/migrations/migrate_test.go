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

func TestUpFreshAndIdempotent(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	// Wipe schema for a clean slate (test DB only).
	_, _ = pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`)
	if err := Up(ctx, pool); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	versions, err := AppliedVersions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) < 4 {
		t.Fatalf("expected >=4 versions, got %v", versions)
	}
	want := []string{"001_init", "002_run_metrics", "003_langgraph", "004_agent_map"}
	for i, w := range want {
		if i >= len(versions) || versions[i] != w {
			t.Fatalf("versions[%d]=%v want %v (all=%v)", i, versions, w, versions)
		}
	}
	// Idempotent
	if err := Up(ctx, pool); err != nil {
		t.Fatalf("second Up: %v", err)
	}
	versions2, _ := AppliedVersions(ctx, pool)
	if len(versions2) != len(versions) {
		t.Fatalf("idempotent mismatch %v vs %v", versions, versions2)
	}
}

func TestUpBootstrapMarksBaselineWithoutRerun(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	_, _ = pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`)
	// Apply raw SQL like today's prod (no schema_migrations).
	files, err := listSQL()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		body, err := FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("raw %s: %v", name, err)
		}
	}
	// Seed a row so we can detect accidental re-create wipe (IF NOT EXISTS wouldn't wipe anyway).
	_, err = pool.Exec(ctx, `INSERT INTO sessions(id, name) VALUES ('11111111-1111-1111-1111-111111111111', 'bootstrap-keep')`)
	if err != nil {
		t.Fatal(err)
	}

	if err := Up(ctx, pool); err != nil {
		t.Fatalf("bootstrap Up: %v", err)
	}
	versions, err := AppliedVersions(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range bootstrapBaseline {
		found := false
		for _, v := range versions {
			if v == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing bootstrap mark %s in %v", w, versions)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE name='bootstrap-keep'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("bootstrap re-ran or wiped data; sessions keep-row count=%d", n)
	}
	// Second Up still clean
	if err := Up(ctx, pool); err != nil {
		t.Fatal(err)
	}
}
