# m3-final ingest artifacts

Used against VPS `at-m3` on `:1985` with LangSmith Python SDK (`langsmith.Client`).

## Primary script
- `ingest_m3_final.py` — project `m3-final`
  - Nested `m3_support_chain` with ChatOpenAI usage via `usage_metadata` + `outputs.llm_output.token_usage`
  - Tool `lookup_order` + second ChatOpenAI
  - LLM1 `start_time` earlier than root (clock skew / out-of-order time)
  - **Out-of-order create:** `early_child_llm` POSTed before parent `oo_parent_chain` (child has `parent_run_id` + dotted_order prefix; observed `trace_id` stayed on child — backend issue noted)
  - Error chain `m3_error_chain`

## Env
```
export LANGSMITH_ENDPOINT=http://<host>:1985
export LANGSMITH_API_KEY=...   # never commit
export LANGSMITH_PROJECT=m3-final
python ingest_m3_final.py
```

## Sample API responses (from that run)
- `sample_nested_summary.json`, `sample_oo_summary.json`, `sample_oo_child_run.json`, `sample_trace_list.json`
- `ingest_final_out.txt` — printed IDs
