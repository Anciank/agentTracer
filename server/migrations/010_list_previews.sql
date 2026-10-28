-- Trace list chat previews (first user + last AI), filled at ingest.
-- Forward-only; empty string means "unknown / not yet".
ALTER TABLE runs ADD COLUMN IF NOT EXISTS first_user_preview TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN IF NOT EXISTS last_ai_preview TEXT NOT NULL DEFAULT '';
