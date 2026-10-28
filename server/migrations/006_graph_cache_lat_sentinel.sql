-- Rebuild trace_graph_cache after node_lat_ms semantics change:
-- unfinished visits store -1; finished visits store duration_ms (>=0, including
-- sub-millisecond 0). Old rows used 0 for both, and /graph skipped latMs>0 only,
-- so fast LangGraph nodes (retrieve/rank/plan) showed null p50_ms/p95_ms.
-- agenttracer:expand-contract
DELETE FROM trace_graph_cache;
