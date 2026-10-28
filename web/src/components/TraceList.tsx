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
  }, [filters.project, filters.q, filters.status, filters.sort]);

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
          // Queue only — list data/order/scroll/selection stay put.
          setPending((prev) => {
            if (prev.some((t) => t.trace_id === ev.data.trace_id)) return prev;
            if (rowIdsRef.current.has(ev.data.trace_id)) return prev;
            return [ev.data, ...prev].slice(0, 500);
          });
          return;
        }
        if (ev.type === "run_upsert") {
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
                const appending = !isOpen; // without open cache we soft-bump counts
                next[idx] = {
                  ...cur,
                  run_count: appending ? cur.run_count + 1 : cur.run_count,
                  error_count:
                    row.has_error || row.status === "error"
                      ? Math.max(cur.error_count, cur.error_count + 1)
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

  const virt = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 56,
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
            return (
              <div
                key={t.trace_id}
                className={`trow ${t.trace_id === selectedId ? "active" : ""} ${
                  t.status === "error" ? "has-error" : ""
                }`}
                style={{
                  position: "absolute",
                  top: 0,
                  left: 0,
                  width: "100%",
                  height: vi.size,
                  transform: `translateY(${vi.start}px)`,
                }}
                onClick={() => onSelect(t.trace_id)}
                onDoubleClick={() => onOpen(t.trace_id)}
              >
                <div>
                  {t.status === "error" ? (
                    <span className="pill err">err</span>
                  ) : t.status === "running" ? (
                    <span className="pill run">run</span>
                  ) : (
                    <span className="pill ok">ok</span>
                  )}
                </div>
                <div>
                  <div className="tname">{t.name}</div>
                  <div className="t2">
                    <span title={t.start}>{fmtRel(t.start)}</span>
                    <span>{t.project}</span>
                    <span>{fmtInt(t.run_count)} runs</span>
                    <span>{fmtInt(t.tokens.total)} tok</span>
                    {t.model && <span>{t.model}</span>}
                    {t.error_count > 0 && <span className="pill err">{t.error_count}</span>}
                  </div>
                </div>
                <div className="tdur">{fmtDurMs(t.duration_ms)}</div>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
