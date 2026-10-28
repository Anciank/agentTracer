import { useEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import { listTraces, openTraceStream, ApiError } from "../api/client";
import type {
  TraceListParams,
  TraceListResponse,
  TraceSummary,
  TraceSummaryResponse,
} from "../api/types";
import { fmtDurMs, fmtInt, fmtRel } from "../lib/format";
import type { UiFilters } from "../lib/filters";
import { applyRunUpsert, mergePendingTraces } from "../lib/liveMerge";
import { ErrorState } from "./ErrorState";

/** Must match `.trow` height in styles.css — virtualizer uses this exactly. */
export const TRACE_ROW_H = 52;

type InfiniteTraces = { pages: TraceListResponse[]; pageParams: unknown[] };

type Props = {
  filters: UiFilters;
  selectedId: string | null;
  openTraceId?: string | null;
  onSelect: (id: string) => void;
  onOpen: (id: string) => void;
  focusRef?: React.MutableRefObject<HTMLDivElement | null>;
  onLiveStatus?: (connected: boolean) => void;
};

export function TraceList({
  filters,
  selectedId,
  openTraceId,
  onSelect,
  onOpen,
  focusRef,
  onLiveStatus,
}: Props) {
  const parentRef = useRef<HTMLDivElement>(null);
  /** New `trace` SSE events wait here until the pill is clicked — list never jumps. */
  const [pending, setPending] = useState<TraceSummary[]>([]);
  const [liveOk, setLiveOk] = useState(false);
  const qc = useQueryClient();
  const openRef = useRef(openTraceId);
  openRef.current = openTraceId;
  const selectedRef = useRef(selectedId);
  selectedRef.current = selectedId;
  const rowIdsRef = useRef<Set<string>>(new Set());

  const params: TraceListParams = useMemo(
    () => ({
      project: filters.project,
      q: filters.q,
      status: filters.status,
      sort: filters.sort || "start_desc",
      limit: 100,
      from: filters.from,
      to: filters.to,
      node: filters.node,
      edge: filters.edge,
      path: filters.path,
      node_fail: filters.node_fail,
    }),
    [filters]
  );

  const q = useInfiniteQuery({
    queryKey: ["traces", params],
    queryFn: ({ pageParam }) => listTraces({ ...params, cursor: pageParam }),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });

  const rows = useMemo(() => q.data?.pages.flatMap((p) => p.traces) ?? [], [q.data]);
  rowIdsRef.current = new Set(rows.map((t) => t.trace_id));

  useEffect(() => {
    setPending([]);
  }, [filters.project, filters.q, filters.status, filters.sort, filters.node, filters.edge, filters.path, filters.node_fail, filters.from, filters.to, filters.model, filters.tool]);

  useEffect(() => {
    if (!filters.live) {
      setPending([]);
      setLiveOk(false);
      onLiveStatus?.(false);
      return;
    }
    const handle = openTraceStream(
      { project: filters.project, q: filters.q, status: filters.status },
      (ev) => {
        if (ev.type === "heartbeat") {
          setLiveOk(true);
          onLiveStatus?.(true);
          return;
        }
        if (ev.type === "trace") {
          // Real server emits `trace` for new roots AND rollup refresh. Only truly
          // new ids queue behind the pill; known rows patch in place (no reorder).
          const tid = ev.data.trace_id;
          if (rowIdsRef.current.has(tid)) {
            qc.setQueryData<InfiniteTraces>(["traces", params], (old) => {
              if (!old) return old;
              let touched = false;
              const pages = old.pages.map((page) => {
                const idx = page.traces.findIndex((t) => t.trace_id === tid);
                if (idx < 0) return page;
                touched = true;
                const next = page.traces.slice();
                next[idx] = { ...page.traces[idx], ...ev.data };
                return { ...page, traces: next };
              });
              return touched ? { ...old, pages } : old;
            });
            return;
          }
          setPending((prev) => {
            const i = prev.findIndex((t) => t.trace_id === tid);
            if (i >= 0) {
              const next = prev.slice();
              next[i] = ev.data;
              return next;
            }
            return [ev.data, ...prev].slice(0, 500);
          });
          return;
        }
        if (ev.type === "run_upsert") {
          // Payload: { trace_id, row: SpanSummary } on every create and PATCH.
          const { trace_id, row } = ev.data;
          const isOpen = openRef.current === trace_id;

          if (isOpen) {
            qc.setQueryData<TraceSummaryResponse>(["summary", trace_id], (old) => {
              if (!old) return old;
              return applyRunUpsert(old, row);
            });
          }

          // Update that trace's summary row in place (no reorder). Prefer open-cache summary.
          qc.setQueryData<InfiniteTraces>(["traces", params], (old) => {
            if (!old) return old;
            let touched = false;
            const pages = old.pages.map((page) => {
              const idx = page.traces.findIndex((t) => t.trace_id === trace_id);
              if (idx < 0) return page;
              touched = true;
              const cur = page.traces[idx];
              const sum = qc.getQueryData<TraceSummaryResponse>(["summary", trace_id]);
              const next = page.traces.slice();
              if (sum) {
                next[idx] = { ...sum.summary };
              } else {
                // Soft status/error bump only — do not +1 run_count (PATCH also upserts).
                next[idx] = {
                  ...cur,
                  error_count:
                    row.has_error || row.status === "error"
                      ? Math.max(cur.error_count, 1)
                      : cur.error_count,
                  status:
                    row.status === "error"
                      ? "error"
                      : cur.status === "running" || row.status === "running"
                        ? cur.status === "error"
                          ? "error"
                          : "running"
                        : cur.status,
                };
              }
              return { ...page, traces: next };
            });
            return touched ? { ...old, pages } : old;
          });
        }
      },
      { getOpenTraceId: () => openRef.current }
    );
    setLiveOk(true);
    onLiveStatus?.(true);
    return () => {
      handle.close();
      setLiveOk(false);
      onLiveStatus?.(false);
    };
  }, [filters.live, filters.project, filters.q, filters.status, params, qc, onLiveStatus]);

  function flushPending() {
    if (!pending.length) return;
    const keepSel = selectedRef.current;
    qc.setQueryData<InfiniteTraces>(["traces", params], (old) => {
      if (!old || !old.pages.length) {
        return {
          pages: [{ traces: pending, next_cursor: null }],
          pageParams: [undefined],
        };
      }
      const pages = old.pages.slice();
      const first = pages[0];
      pages[0] = { ...first, traces: mergePendingTraces(first.traces, pending) };
      return { ...old, pages };
    });
    setPending([]);
    if (keepSel) onSelect(keepSel);
  }

  const hideProject = Boolean(filters.project);

  const virt = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => TRACE_ROW_H,
    overscan: 12,
  });

  useEffect(() => {
    if (focusRef) focusRef.current = parentRef.current;
  }, [focusRef]);

  function onScroll() {
    const el = parentRef.current;
    if (!el) return;
    if (
      el.scrollTop + el.clientHeight > el.scrollHeight - 200 &&
      q.hasNextPage &&
      !q.isFetchingNextPage
    ) {
      void q.fetchNextPage();
    }
  }

  return (
    <div className="list-wrap">
      {filters.live && pending.length > 0 && (
        <button type="button" className="new-pill" onClick={flushPending}>
          {pending.length} new
        </button>
      )}
      {filters.live && (
        <div className="live-status" title={liveOk ? "stream connected" : "connecting…"}>
          <span className={`live-dot ${liveOk ? "" : "off"}`} />
          {liveOk ? "live" : "…"}
        </div>
      )}
      <div className="list" ref={parentRef} onScroll={onScroll}>
        {q.isLoading && <div className="empty">Loading traces…</div>}
        {q.isError && (
          <ErrorState
            error={q.error}
            onRetry={() => {
              void q.refetch();
            }}
          />
        )}
        {!q.isLoading && !q.isError && rows.length === 0 && (
          <div className="empty">No traces match</div>
        )}
        <div style={{ height: virt.getTotalSize(), position: "relative" }}>
          {virt.getVirtualItems().map((vi) => {
            const t = rows[vi.index];
            const running = t.status === "running";
            const errored = t.status === "error";
            const meta: string[] = [];
            meta.push(fmtRel(t.start));
            if (!hideProject && t.project) meta.push(t.project);
            meta.push(`${fmtInt(t.run_count)} runs`);
            meta.push(`${fmtInt(t.tokens.total)} tok`);
            return (
              <div
                key={t.trace_id}
                role="option"
                aria-selected={t.trace_id === selectedId || t.trace_id === openTraceId}
                className={`trow ${t.trace_id === selectedId || t.trace_id === openTraceId ? "active" : ""} ${
                  errored ? "has-error" : ""
                } ${running ? "is-running" : ""}`}
                style={{
                  position: "absolute",
                  top: 0,
                  left: 0,
                  width: "100%",
                  height: TRACE_ROW_H,
                  transform: `translateY(${vi.start}px)`,
                }}
                onClick={() => onOpen(t.trace_id)}
                onDoubleClick={() => onOpen(t.trace_id)}
              >
                <span
                  className={`tstatus ${errored ? "err" : running ? "run" : "ok"}`}
                  title={t.status}
                  aria-label={t.status}
                />
                <div className="tbody">
                  <div className="tline1">
                    <div className="tname" title={t.name}>
                      {t.name}
                    </div>
                    <div className="tdur" title={running ? "running" : undefined}>
                      {running ? (
                        <span className="tdur-run" aria-label="running">
                          …
                        </span>
                      ) : (
                        fmtDurMs(t.duration_ms)
                      )}
                    </div>
                  </div>
                  <div className="tline2">
                    <div className="tmeta" title={meta.join(" · ")}>
                      {meta.map((part, i) => (
                        <span key={i} className="tmeta-part">
                          {i > 0 ? <span className="tdot">·</span> : null}
                          {part}
                        </span>
                      ))}
                      {t.model ? (
                        <span className="tmeta-part">
                          <span className="tdot">·</span>
                          <span className="tmodel" title={t.model}>
                            {t.model}
                          </span>
                        </span>
                      ) : null}
                    </div>
                    {t.error_count > 0 ? (
                      <span className="terr" title={`${t.error_count} errors`}>
                        {fmtInt(t.error_count)}
                      </span>
                    ) : null}
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
