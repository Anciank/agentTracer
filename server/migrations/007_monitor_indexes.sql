-- M3.6 Monitor: indexes for /monitor by_model / by_tool and tool= filter.
-- Idempotent; safe to re-run.

CREATE INDEX IF NOT EXISTS idx_runs_session_runtype_start
  ON runs (session_id, run_type, start_time DESC NULLS LAST)
  WHERE start_time IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_runs_trace_runtype_name
  ON runs (trace_id, run_type, name)
  WHERE run_type IN ('tool', 'llm');
