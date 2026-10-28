# Python `tests/` → Go rewrite coverage (M2)

| Python test | Destination | Notes |
|---|---|---|
| **test_server.py — multipart parse** | | |
| `test_multipart_parses_fragmented_fields_and_binary_attachments` | Go `TestMultipartFragmentedAndBinary` | unit |
| `test_multipart_handles_payload_containing_crlf` | Go `TestMultipartCRLFInPayload` | unit |
| `test_multipart_strips_attachment_length_parameter` | Go `TestAttachmentLengthParam` | unit |
| `test_multipart_separates_posts_patches_and_feedback` | Go `TestPostsPatchesFeedback` | unit |
| `test_batch_from_json_accepts_dicts_and_skips_id_less_payloads` | Go `TestBatchFromJSON` | unit |
| **test_server.py — merge / storage** | | |
| `test_patch_merges_into_existing_run_without_wiping_fields` | compat `test_patch_null_merge` + Go `TestWireQueryFeedbackSessionsHealth` | |
| `test_error_string_is_persisted_and_queryable` | compat `test_error_persisted_and_queryable` | |
| `test_run_auto_creates_and_joins_a_project` | compat `test_run_auto_creates_session` | |
| `test_query_filters_by_trace_parent_type_and_limit` | compat `test_query_filters` | |
| `test_attachments_round_trip_binary_and_record_metadata` | compat `test_attachment_round_trip` + Go `TestMultipartIngestAndUI` | |
| **test_server.py — HTTP** | | |
| `test_info_advertises_a_version_the_sdk_accepts` | compat `test_info_requires_key_and_reports_multipart` | |
| `test_sessions_endpoint_returns_tenant_id_required_by_sdk` | compat `test_sessions_crud` | expanded to full CRUD |
| `test_multipart_endpoint_ingests_and_query_returns_cursor_shape` | compat `test_multipart_and_query_cursor_shape` | expects 204 |
| `test_multipart_endpoint_rejects_missing_boundary` | compat `test_multipart_rejects_missing_boundary` | |
| `test_feedback_round_trip` | compat `test_feedback_round_trip` | run must exist (FK) |
| `test_health_reports_stored_counts` | Go `TestHealthNoCounts` + compat `test_health_ok_no_counts` | **intentional M2 change**: no counts |
| `test_create_run_without_id_is_rejected` | compat `test_create_run_without_id_rejected` | |
| **test_server.py — request log** | | |
| `test_request_log_records_method_status_and_full_payload` | **skip** | request log not ported to Go |
| `test_request_log_decodes_multipart_into_readable_parts` | **skip** | request log not ported |
| `test_request_log_is_skipped_when_path_is_empty` | **skip** | request log not ported |
| `test_request_log_truncates_oversized_payloads` | **skip** | request log not ported |
| **gzip (M2 requirement; not in Python suite)** | | |
| gzip single `POST /runs` | compat `test_gzip_single_post` + Go multipart gzip | |
| gzip `POST /runs/batch` | compat `test_gzip_batch` | |
| gzip `POST /runs/multipart` | compat `test_gzip_multipart` + existing zstd | |
| **test_ui.py — derived** | | |
| `test_depth_comes_from_the_dotted_order` | Go `TestTraceDetailWaterfallShape` | |
| `test_depth_falls_back_to_walking_parents` | Go `TestTraceDetailWaterfallShape` | |
| `test_status_distinguishes_error_running_and_success` | Go traces package | |
| `test_usage_prefers_usage_metadata_then_falls_back_to_llm_output` | Go `TestExtractUsageFromExtra` | |
| `test_trace_detail_orders_rows_and_computes_offsets` | Go `TestTraceDetailWaterfallShape` | |
| `test_self_time_subtracts_direct_children` | Go `TestTraceDetailWaterfallShape` | |
| `test_self_time_never_goes_negative_when_children_overlap` | Go `TestTraceDetailWaterfallShape` | |
| `test_unfinished_run_is_marked_running` | Go traces | |
| `test_trace_detail_returns_none_without_a_start_time` | Go traces | |
| `test_summaries_roll_up_counts_and_tokens` | Go UI queries / compat UI | |
| **test_ui.py — HTTP (legacy `/ui`)** | | |
| `test_index_serves_the_page` | compat `test_ui_index` | |
| `test_projects_lists_only_named_projects` | compat `test_api_ui_projects` (`/api/ui` frozen) | |
| `test_traces_endpoint_returns_summaries` | compat `test_api_ui_traces` | |
| `test_traces_endpoint_filters_by_project` | compat `test_api_ui_traces` | |
| `test_unknown_project_returns_nothing_not_everything` | compat `test_api_ui_unknown_project` | |
| `test_trace_list_is_not_truncated_by_the_run_row_cap` | **skip** | needs large fixture; covered conceptually by Unlimited QueryOpts |
| `test_query_runs_limit_none_removes_the_cap` | Go store Unlimited + compat query limit | |
| `test_trace_detail_endpoint_returns_positioned_rows` | compat `test_api_ui_trace_summary` | |
| `test_trace_detail_endpoint_404s_for_unknown_trace` | compat `test_api_ui_trace_summary_404` | |
| `test_ui_endpoints_do_not_require_the_langsmith_sdk_headers` | compat via x-api-key only | UI gated by API key in M2/M3 |
| **test_analysis.py (7)** | **skip (all)** | `analyze` CLI deferred to M6; not in Go M2 surface |
| `test_identical_failures_are_grouped_with_counts` | skip | M6 |
| `test_no_errors_yields_no_groups` | skip | M6 |
| `test_report_caps_rendered_trace_trees` | skip | M6 |
| `test_max_traces_zero_renders_everything` | skip | M6 |
| `test_report_caps_rendered_error_groups` | skip | M6 |
| `test_empty_report_says_so` | skip | M6 |
| `test_report_of_a_single_trace_still_renders_its_tree` | skip | M6 |

## Totals (Python `tests/`)
- test_server.py: 24
- test_ui.py: 20
- test_analysis.py: 7
- **Total: 51**

Coverage intent: port wire/HTTP behaviours into `compat/`; keep pure parse/trace math in Go unit tests; skip request-log + analysis.
