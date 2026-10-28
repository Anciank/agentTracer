-- M3.5 Agent map: indexes for /graph and /paths over multi-day windows.
-- path_fingerprint already exists (003); we index roots and visit ordering.

CREATE INDEX IF NOT EXISTS idx_runs_session_root_start
  ON runs(session_id, start_time DESC NULLS LAST)
  WHERE parent_run_id IS NULL;

CREATE INDEX IF NOT EXISTS idx_runs_session_fingerprint
  ON runs(session_id, path_fingerprint, start_time DESC NULLS LAST)
  WHERE parent_run_id IS NULL AND path_fingerprint IS NOT NULL AND path_fingerprint <> '';

-- Visit ordering within a trace (langgraph_step, then dotted_order).
CREATE INDEX IF NOT EXISTS idx_runs_trace_visit
  ON runs(trace_id, langgraph_step NULLS LAST, dotted_order);

-- Session-scoped window scans of skinny visit rows.
CREATE INDEX IF NOT EXISTS idx_runs_session_start_trace
  ON runs(session_id, start_time DESC NULLS LAST, trace_id)
  WHERE start_time IS NOT NULL;
