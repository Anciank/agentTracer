-- M4 follow-up: allow NULLing extra/events after cold archive.
-- Idempotent if 011 already dropped these constraints on a fresh DB.
ALTER TABLE runs ALTER COLUMN extra DROP NOT NULL;
ALTER TABLE runs ALTER COLUMN events DROP NOT NULL;
