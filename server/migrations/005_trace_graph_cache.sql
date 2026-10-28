-- M3.5 perf: per-trace visit sequence maintained when the root ends.
-- /graph aggregates from this skinny table instead of scanning all runs.

CREATE TABLE IF NOT EXISTS trace_graph_cache (
  trace_id      UUID PRIMARY KEY,
  session_id    UUID NOT NULL,
  root_start    TIMESTAMPTZ NOT NULL,
  node_keys     TEXT[] NOT NULL,
  node_from_lg  BOOLEAN[] NOT NULL,
  node_errors   BOOLEAN[] NOT NULL,
  node_lat_ms   DOUBLE PRECISION[] NOT NULL,
  node_in       INT[] NOT NULL,
  node_out      INT[] NOT NULL,
  node_tot      INT[] NOT NULL,
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_tgc_session_start
  ON trace_graph_cache (session_id, root_start DESC);
