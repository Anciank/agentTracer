/**
 * In-place Monitor refresh: apply new payload without resetting UI chrome
 * (hover, sort, scroll, pinned tooltip).
 */
import type { MonitorResponse } from "../api/types";

export type MonitorTableSort = {
  key: string;
  dir: "asc" | "desc";
};

export type MonitorUiState = {
  /** Chart hover bucket index (or null). */
  hoverIdx: number | null;
  /** Which series is hovered: traces | latency | tokens */
  hoverSeries: "traces" | "latency" | "tokens" | null;
  /** Tooltip pinned (click-hold) — pause polling while true. */
  tooltipPinned: boolean;
  modelSort: MonitorTableSort;
  toolSort: MonitorTableSort;
  /** Scroll position of the monitor scroller (px). */
  scrollTop: number;
};

export const DEFAULT_MONITOR_UI: MonitorUiState = {
  hoverIdx: null,
  hoverSeries: null,
  tooltipPinned: false,
  modelSort: { key: "calls", dir: "desc" },
  toolSort: { key: "calls", dir: "desc" },
  scrollTop: 0,
};

/**
 * Merge a poll result into current data + UI.
 * - Replaces data wholesale (new reference) when changed.
 * - Preserves sort, scroll, pinned, and clamps hover to new series length.
 * - When `unchanged`, returns prev references so React skips work.
 */
export function applyMonitorRefresh(
  prev: MonitorResponse | null,
  next: MonitorResponse | null,
  ui: MonitorUiState,
  opts: { unchanged?: boolean } = {}
): { data: MonitorResponse | null; ui: MonitorUiState } {
  if (opts.unchanged || next == null) {
    return { data: prev, ui };
  }
  if (prev && monitorFingerprint(prev) === monitorFingerprint(next)) {
    return { data: prev, ui };
  }
  const len = next.series_traces.length;
  let hoverIdx = ui.hoverIdx;
  if (hoverIdx != null && (hoverIdx < 0 || hoverIdx >= len)) hoverIdx = null;
  return {
    data: next,
    ui: {
      ...ui,
      hoverIdx,
      // keep hoverSeries, tooltipPinned, sorts, scrollTop
    },
  };
}

export function monitorFingerprint(m: MonitorResponse): string {
  const last = m.series_traces[m.series_traces.length - 1];
  return [
    m.project_id,
    m.window,
    m.from,
    m.to,
    m.trace_count,
    m.error_count,
    m.success_count,
    m.tokens.total,
    last?.t ?? "",
    last?.total ?? 0,
    m.by_model.length,
    m.by_tool.length,
  ].join("|");
}

/** Whether the live poll should run right now. */
export function shouldPollMonitor(ui: MonitorUiState, tabVisible: boolean): boolean {
  if (!tabVisible) return false;
  if (ui.tooltipPinned) return false;
  return true;
}
