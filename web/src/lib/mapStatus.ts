import { ApiError } from "../api/client";

/** /graph or /paths not implemented on this server build (404 or 501). */
export function isMapEndpointMissing(error: unknown): boolean {
  return error instanceof ApiError && (error.status === 404 || error.status === 501);
}
