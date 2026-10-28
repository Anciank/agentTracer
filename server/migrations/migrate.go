package migrations

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Advisory lock key unique to agentTracer migrations (arbitrary stable int64).
const advisoryLockKey int64 = 8720141984

// bootstrapBaseline are versions that already exist on prod before schema_migrations.
// On first Up against a DB that already has `runs`, these are marked applied without re-executing.
var bootstrapBaseline = []string{
	"001_init",
	"002_run_metrics",
	"003_langgraph",
	"004_agent_map",
}

const ensureTableSQL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

// Up applies forward-only numbered *.sql migrations under an advisory lock.
// Each pending migration runs in its own transaction. Already-applied versions are skipped.
// If schema_migrations is empty but the `runs` table exists, bootstrapBaseline versions
// are recorded as applied without re-running (prod bootstrap).
func Up(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockKey); err != nil {
		return fmt.Errorf("advisory lock: %w", err)
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryLockKey) }()

	if _, err := conn.Exec(ctx, ensureTableSQL); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	applied, err := loadApplied(ctx, conn)
	if err != nil {
		return err
	}

	files, err := listSQL()
	if err != nil {
		return err
	}

	bootstrapping := len(applied) == 0 && tableExists(ctx, conn, "runs")
	baseline := map[string]struct{}{}
	if bootstrapping {
		for _, v := range bootstrapBaseline {
			baseline[v] = struct{}{}
		}
	}

	for _, name := range files {
		version := strings.TrimSuffix(name, ".sql")
		if _, ok := applied[version]; ok {
			continue
		}

		body, err := FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		sqlText := string(body)
		if err := CheckDestructive(name, sqlText); err != nil {
			return err
		}

		if _, inBaseline := baseline[version]; inBaseline {
			if _, err := conn.Exec(ctx,
				`INSERT INTO schema_migrations(version) VALUES ($1) ON CONFLICT DO NOTHING`, version); err != nil {
				return fmt.Errorf("bootstrap mark %s: %w", version, err)
			}
			applied[version] = struct{}{}
			continue
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx, sqlText); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migrate %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations(version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record %s: %w", version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit %s: %w", version, err)
		}
		applied[version] = struct{}{}
	}
	return nil
}

func listSQL() ([]string, error) {
	entries, err := FS.ReadDir(".")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, ".sql") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names, nil
}

func loadApplied(ctx context.Context, conn *pgxpool.Conn) (map[string]struct{}, error) {
	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("select schema_migrations: %w", err)
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = struct{}{}
	}
	return out, rows.Err()
}

func tableExists(ctx context.Context, conn *pgxpool.Conn, name string) bool {
	var exists bool
	_ = conn.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name=$1)`,
		name,
	).Scan(&exists)
	return exists
}

// AppliedVersions returns applied migration versions (for tests/ops).
func AppliedVersions(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
