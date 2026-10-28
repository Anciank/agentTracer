// Package dbtest provides a shared, prod-safe Postgres DSN helper for tests.
//
// DB-touching tests MUST use AGENTTRACER_TEST_DATABASE_URL only. They must never
// read AGENTTRACER_DATABASE_URL (the production app env), which would make
// DROP/TRUNCATE against prod possible if someone runs `go test` with prod env.
package dbtest

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// EnvTestDatabaseURL is the only env var DB tests may read for a DSN.
const EnvTestDatabaseURL = "AGENTTRACER_TEST_DATABASE_URL"

// AllowTestDatabaseName reports whether a database name is safe for destructive tests.
// Allowed: names ending in "_test" or "scratch" (e.g. agenttracer_test, scratch, foo_scratch).
func AllowTestDatabaseName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	return strings.HasSuffix(name, "_test") || strings.HasSuffix(name, "scratch")
}

// DatabaseNameFromDSN extracts the database name from a postgres URL or keyword DSN.
func DatabaseNameFromDSN(dsn string) (string, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return "", fmt.Errorf("empty DSN")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err == nil && cfg.ConnConfig.Database != "" {
		return cfg.ConnConfig.Database, nil
	}
	// Fallback for odd URLs ParseConfig rejects but url.Parse can still read.
	if strings.Contains(dsn, "://") {
		u, uerr := url.Parse(dsn)
		if uerr != nil {
			if err != nil {
				return "", fmt.Errorf("parse DSN: %w", err)
			}
			return "", uerr
		}
		name := strings.TrimPrefix(u.Path, "/")
		if i := strings.IndexByte(name, '/'); i >= 0 {
			name = name[:i]
		}
		if name == "" {
			return "", fmt.Errorf("DSN has no database name in path")
		}
		return name, nil
	}
	if err != nil {
		return "", fmt.Errorf("parse DSN: %w", err)
	}
	return "", fmt.Errorf("DSN has no database name")
}

// ValidateTestDatabaseURL checks a DSN is present and names a test/scratch database.
// Returns ("", nil) semantics are not used — empty dsn returns a skip-sentinel error?
// Callers that need skip-vs-fatal should use TestDatabaseURL.
func ValidateTestDatabaseURL(dsn string) error {
	if strings.TrimSpace(dsn) == "" {
		return fmt.Errorf("%s not set", EnvTestDatabaseURL)
	}
	name, err := DatabaseNameFromDSN(dsn)
	if err != nil {
		return fmt.Errorf("%s: %w", EnvTestDatabaseURL, err)
	}
	if !AllowTestDatabaseName(name) {
		return fmt.Errorf("%s database name %q is not allowed for tests (must end in _test or scratch); refusing to run destructive tests", EnvTestDatabaseURL, name)
	}
	return nil
}

// TestDatabaseURL returns AGENTTRACER_TEST_DATABASE_URL after safety checks.
// Skips the test if unset. Fatals if the database name is not a test/scratch DB.
// Never reads AGENTTRACER_DATABASE_URL.
func TestDatabaseURL(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv(EnvTestDatabaseURL)
	if strings.TrimSpace(dsn) == "" {
		t.Skip(EnvTestDatabaseURL + " not set")
	}
	if err := ValidateTestDatabaseURL(dsn); err != nil {
		t.Fatal(err)
	}
	return dsn
}
