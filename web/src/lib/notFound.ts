import { ApiError } from "../api/client";

/** HTTP 404 from /api/ui — expected empty state, not a hard failure. */
export function isNotFoundError(error: unknown): boolean {
  return error instanceof ApiError && error.status === 404;
}
