import { useEffect, useMemo, useRef, useState } from "react";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import { listTraces, openTraceStream } from "../api/client";
import type { TraceSummary, TraceListParams } from "../api/types";
import { fmtDurMs, fmtInt, fmtRel } from "../lib/format";
import type { UiFilters } from "../lib/filters";

type Props = {
  filters: UiFilters;
  selectedId: string | null;
  onSelect: (id: string) => void;
  onOpen: (id: string) => void;
  focusRef?: React.MutableRefObject<HTMLDivElement | null>;
};

export function TraceList({ filters, selectedId, onSelect, onOpen, focusRef }: Props) {
  const parentRef = useRef<HTMLDivElement>(null);
  const [liveRows, setLiveRows] = useState<TraceSummary[]>([]);
  const [paused, setPaused] = useState(false);
  const qc = useQueryClient();

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

  const serverRows = useMemo(
    () => q.data?.pages.flatMap((p) => p.traces) ?? [],
    [q.data]
  );

  const rows = useMemo(() => {
    const seen = new Set(liveRows.map((t) => t.trace_id));
    return [...liveRows, ...serverRows.filter((t) => !seen.has(t.trace_id))];
  }, [liveRows, serverRows]);

  useEffect(() => {
    if (!filters.live) {
      setLiveRows([]);
      return;
    }
    const handle = openTraceStream(
      { project: filters.project, q: filters.q, status: filters.status },
      (ev) => {
        if (ev.type === "trace") {
          setLiveRows((prev) => {
            if (prev.some((t) => t.trace_id === ev.data.trace_id)) return prev;
            return [{ ...ev.data, /* mark */ } as TraceSummary, ...prev].slice(0, 200);
          });
          if (!paused && parentRef.current && parentRef.current.scrollTop < 40) {
            parentRef.current.scrollTop = 0;
          }
        }
      }
    );
    return () => handle.close();
  }, [filters.live, filters.project, filters.q, filters.status, paused]);

  // clear live cache when filters change non-live
  useEffect(() => {
    setLiveRows([]);
  }, [filters.project, filters.q, filters.status, filters.sort]);

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
    setPaused(el.scrollTop > 80);
    if (el.scrollTop + el.clientHeight > el.scrollHeight - 200 && q.hasNextPage && !q.isFetchingNextPage) {
      void q.fetchNextPage();
    }
  }

  return (
    <div className="list" ref={parentRef} onScroll={onScroll}>
      {q.isLoading && <div className="empty">Loading traces…</div>}
      {!q.isLoading && rows.length === 0 && <div className="empty">No traces match</div>}
      <div style={{ height: virt.getTotalSize(), position: "relative" }}>
        {virt.getVirtualItems().map((vi) => {
          const t = rows[vi.index];
          const isLive = liveRows.some((l) => l.trace_id === t.trace_id);
          return (
            <div
              key={t.trace_id}
              className={`trow ${t.trace_id === selectedId ? "active" : ""} ${
                t.status === "error" ? "has-error" : ""
              } ${isLive ? "new" : ""}`}
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
      {/* keep qc referenced to avoid unused lint in some setups */}
      <span style={{ display: "none" }}>{String(!!qc)}</span>
    </div>
  );
}
