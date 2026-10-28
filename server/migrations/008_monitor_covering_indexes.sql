-- M3.6 Monitor perf: covering indexes for /monitor by_model / by_tool / tokens
-- at ~5k real LangGraph traces (llm+tool children). Replaces 007's
-- idx_runs_trace_runtype_name which steered the planner into global run_type
-- bitmap heaps (~200–370ms each). Idempotent; safe to re-run.

DROP INDEX IF EXISTS idx_runs_trace_runtype_name;

CREATE INDEX IF NOT EXISTS idx_runs_session_llm_monitor
  ON runs (session_id, start_time DESC NULLS LAST)
  INCLUDE (model, input_tokens, output_tokens, total_tokens, end_time, error, trace_id)
  WHERE run_type = 'llm' AND start_time IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_runs_session_tool_monitor
  ON runs (session_id, start_time DESC NULLS LAST)
  INCLUDE (name, total_tokens, end_time, error, trace_id)
  WHERE run_type = 'tool' AND start_time IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_runs_trace_llm_tokens
  ON runs (trace_id)
  INCLUDE (input_tokens, output_tokens, total_tokens)
  WHERE run_type = 'llm';

-- Narrow replacements for tool=/model= EXISTS filters on /api/ui/traces.
CREATE INDEX IF NOT EXISTS idx_runs_trace_tool_name
  ON runs (trace_id, name)
  WHERE run_type = 'tool';

CREATE INDEX IF NOT EXISTS idx_runs_trace_llm_model
  ON runs (trace_id, model)
  WHERE run_type = 'llm';
