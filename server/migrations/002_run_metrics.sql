-- Denormalized metrics for UI list/stats (M2)
ALTER TABLE runs ADD COLUMN IF NOT EXISTS input_tokens INT NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS output_tokens INT NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS total_tokens INT NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS model TEXT NOT NULL DEFAULT '';
ALTER TABLE feedback ADD COLUMN IF NOT EXISTS trace_id UUID;
CREATE INDEX IF NOT EXISTS idx_runs_model ON runs(model) WHERE model <> '';
CREATE INDEX IF NOT EXISTS idx_runs_session_start ON runs(session_id, start_time DESC NULLS LAST);
