-- agentTracer M1 schema (Postgres)
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS sessions (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    start_time TIMESTAMPTZ NOT NULL DEFAULT now(),
    end_time TIMESTAMPTZ,
    extra JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE IF NOT EXISTS runs (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    run_type TEXT NOT NULL DEFAULT 'chain',
    session_id UUID REFERENCES sessions(id) ON DELETE SET NULL,
    parent_run_id UUID,
    dotted_order TEXT NOT NULL DEFAULT '',
    trace_id UUID,
    start_time TIMESTAMPTZ,
    end_time TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'pending',
    error TEXT,
    inputs JSONB,
    outputs JSONB,
    extra JSONB NOT NULL DEFAULT '{}'::jsonb,
    events JSONB NOT NULL DEFAULT '[]'::jsonb,
    tags JSONB NOT NULL DEFAULT '[]'::jsonb,
    serialized JSONB,
    inputs_preview TEXT NOT NULL DEFAULT '',
    outputs_preview TEXT NOT NULL DEFAULT '',
    reference_example_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_runs_session ON runs(session_id);
CREATE INDEX IF NOT EXISTS idx_runs_trace ON runs(trace_id);
CREATE INDEX IF NOT EXISTS idx_runs_parent ON runs(parent_run_id);
CREATE INDEX IF NOT EXISTS idx_runs_start ON runs(start_time DESC NULLS LAST);
CREATE INDEX IF NOT EXISTS idx_runs_dotted ON runs(dotted_order);

CREATE TABLE IF NOT EXISTS feedback (
    id UUID PRIMARY KEY,
    run_id UUID REFERENCES runs(id) ON DELETE CASCADE,
    key TEXT NOT NULL DEFAULT '',
    score DOUBLE PRECISION,
    value JSONB,
    comment TEXT NOT NULL DEFAULT '',
    correction JSONB,
    feedback_source JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_feedback_run ON feedback(run_id);

CREATE TABLE IF NOT EXISTS attachments (
    id BIGSERIAL PRIMARY KEY,
    run_id UUID NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    size_bytes BIGINT NOT NULL DEFAULT 0,
    storage_key TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(run_id, name)
);

CREATE INDEX IF NOT EXISTS idx_attachments_run ON attachments(run_id);
