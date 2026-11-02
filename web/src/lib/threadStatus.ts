import { ApiError } from "../api/client";

/** /threads not implemented on this server build (404 or 501). */
export function isThreadsEndpointMissing(error: unknown): boolean {
  return error instanceof ApiError && (error.status === 404 || error.status === 501);
}

export function threadKeySourceLabel(source: string): string {
  if (source === "session_id") return "grouped by metadata.session_id";
  if (source === "thread_id") return "grouped by metadata.thread_id";
  return `grouped by metadata.${source}`;
}

/** Short display form of a thread id (copy still uses full id). */
export function shortThreadId(id: string): string {
  if (id.length <= 12) return id;
  return id.slice(0, 8);
}
