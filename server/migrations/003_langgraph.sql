-- M3.5 Agent map prep: denormalized LangGraph node/step + path fingerprint placeholder
ALTER TABLE runs ADD COLUMN IF NOT EXISTS langgraph_node TEXT NULL;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS langgraph_step INT NULL;
ALTER TABLE runs ADD COLUMN IF NOT EXISTS path_fingerprint TEXT NULL;
-- Used by project graph/path aggregations (session × node over time).
CREATE INDEX IF NOT EXISTS idx_runs_session_lg_node_start
  ON runs(session_id, langgraph_node, start_time DESC NULLS LAST)
  WHERE langgraph_node IS NOT NULL;
