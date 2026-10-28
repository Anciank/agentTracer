-- Conversation thread key denormalized onto root runs (from extra.metadata).
-- Nullable: traces without metadata thread_id/session_id stay unthreaded (hidden from /api/ui/threads).
-- Forward-only; IF NOT EXISTS so re-apply is safe.
ALTER TABLE runs ADD COLUMN IF NOT EXISTS thread_id TEXT;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS thread_key_source TEXT;

-- Partial composite for thread list: project + thread + recency (roots with a key only).
CREATE INDEX IF NOT EXISTS idx_runs_session_thread_start
    ON runs (session_id, thread_id, start_time DESC NULLS LAST)
    WHERE parent_run_id IS NULL AND thread_id IS NOT NULL;
