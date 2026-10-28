-- M4 cold tier: per-trace zstd JSONL blobs in S3/silo; expand-only.
-- Heavy columns (inputs/outputs/events/extra/serialized) may be NULLed after archive.
-- Hot index keeps error, name, run_type, status, timings, tokens, model, tags,
-- langgraph_*, path_fingerprint, previews. No TTL delete in this migration.
-- runs.updated_at already exists (001_init) and is bumped on every UpsertRun.

CREATE TABLE IF NOT EXISTS trace_cold (
    trace_id     UUID PRIMARY KEY,
    cold_uri     TEXT NOT NULL,
    cold_sha256  TEXT NOT NULL,
    cold_bytes   BIGINT NOT NULL DEFAULT 0,
    cold_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    run_count    INT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_trace_cold_at ON trace_cold (cold_at DESC);

-- Allow NULLing heavy cols after archive (extra/events were NOT NULL in 001).
ALTER TABLE runs ALTER COLUMN extra DROP NOT NULL;
ALTER TABLE runs ALTER COLUMN events DROP NOT NULL;

-- Archiver eligibility: skip traces with any recent ingest/update.
CREATE INDEX IF NOT EXISTS idx_runs_trace_updated
  ON runs (trace_id, updated_at DESC);
