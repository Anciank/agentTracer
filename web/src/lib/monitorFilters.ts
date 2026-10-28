/**
 * Map Monitor chart/table clicks → /traces deep-link query params.
 */
import type { TraceStatus } from "../api/types";

export type MonitorDrill = {
  project: string;
  from: string;
  to: string;
  status?: TraceStatus;
  model?: string;
  tool?: string;
};

/** Build `/traces?…` href for a Monitor drill-down. */
export function tracesHref(d: MonitorDrill): string {
  const qs = new URLSearchParams();
  if (d.project) qs.set("project", d.project);
  if (d.from) qs.set("from", d.from);
  if (d.to) qs.set("to", d.to);
  if (d.status) qs.set("status", d.status);
  if (d.model) qs.set("model", d.model);
  if (d.tool) qs.set("tool", d.tool);
  return `/traces?${qs.toString()}`;
}

/** Stacked bar: success or error segment for one bucket. */
export function drillTraceSegment(
  project: string,
  t: string,
  tEnd: string,
  segment: "success" | "error"
): MonitorDrill {
  return { project, from: t, to: tEnd, status: segment };
}

/** Latency / tokens point → bucket window only. */
export function drillBucket(project: string, t: string, tEnd: string): MonitorDrill {
  return { project, from: t, to: tEnd };
}

/** Models table row (optionally errors-only). */
export function drillModel(
  project: string,
  from: string,
  to: string,
  model: string,
  errorsOnly = false
): MonitorDrill {
  const d: MonitorDrill = { project, from, to, model };
  if (errorsOnly) d.status = "error";
  return d;
}

/** Tools table row (optionally errors-only). */
export function drillTool(
  project: string,
  from: string,
  to: string,
  tool: string,
  errorsOnly = false
): MonitorDrill {
  const d: MonitorDrill = { project, from, to, tool };
  if (errorsOnly) d.status = "error";
  return d;
}

/** Flat params object for listTraces / URLSearchParams. */
export function drillToParams(d: MonitorDrill): Record<string, string> {
  const out: Record<string, string> = {};
  if (d.project) out.project = d.project;
  if (d.from) out.from = d.from;
  if (d.to) out.to = d.to;
  if (d.status) out.status = d.status;
  if (d.model) out.model = d.model;
  if (d.tool) out.tool = d.tool;
  return out;
}
