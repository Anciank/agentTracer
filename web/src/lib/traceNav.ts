import { filtersToSearch, type UiFilters } from "./filters";

/** Path used when opening/switching a trace from the list (click or Enter). */
export function traceOpenPath(id: string, filters: UiFilters): string {
  return `/traces/${id}${filtersToSearch(filters)}`;
}

/**
 * Activate target for click / Enter / o.
 * Always opens the highlighted row — even when another trace is already open
 * (previously Enter was a no-op while deepTrace was set, leaving a stale pane).
 */
export function activateOpenId(listSel: string | null, _deepTrace: string | null): string | null {
  return listSel;
}
