import type { TraceStatus } from "../api/types";

/** CSS class for the list status dot / pill. */
export function statusDotClass(status: TraceStatus | string): string {
  switch (status) {
    case "error":
      return "err";
    case "running":
      return "run";
    case "stale":
      return "stale";
    default:
      return "ok";
  }
}

/** Tooltip for status indicators. */
export function statusTitle(status: TraceStatus | string): string {
  if (status === "stale") return "No end time recorded";
  return status;
}

export function isTraceStatus(v: string | null | undefined): v is TraceStatus {
  return v === "success" || v === "error" || v === "running" || v === "stale";
}

/** True when either chat preview field has text. */
export function hasChatPreview(t: {
  first_user_preview?: string | null;
  last_ai_preview?: string | null;
}): boolean {
  return Boolean(t.first_user_preview) || Boolean(t.last_ai_preview);
}

/** Compact one-line preview: "user → reply", omitting null parts. */
export function formatChatPreview(t: {
  first_user_preview?: string | null;
  last_ai_preview?: string | null;
}): string {
  const u = (t.first_user_preview || "").trim();
  const a = (t.last_ai_preview || "").trim();
  if (u && a) return `${u} → ${a}`;
  return u || a;
}
