-- Shared failed-trace definition support: any run with error text or status='error'.
-- Partial index speeds EXISTS (SELECT 1 FROM runs WHERE trace_id=… AND failed).
-- Forward-only, non-destructive.

CREATE INDEX IF NOT EXISTS idx_runs_trace_failed
  ON runs (trace_id)
  WHERE (error IS NOT NULL AND error <> '') OR status = 'error';
