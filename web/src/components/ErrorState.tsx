import { ApiError } from "../api/client";

type Props = {
  error: unknown;
  onRetry?: () => void;
  title?: string;
};

export function ErrorState({ error, onRetry, title }: Props) {
  const status = error instanceof ApiError ? error.status : undefined;
  const detail =
    error instanceof ApiError
      ? error.detail
      : error instanceof Error
        ? error.message
        : "Unknown error";

  let headline = title || "Request failed";
  let hint = detail;
  if (status === 401) {
    headline = "API key required";
    hint = "Enter an x-api-key to talk to the server.";
  } else if (status === 0) {
    headline = "Cannot reach API";
    hint = detail || "Network error — is the server up?";
  } else if (status === 404) {
    headline = "Not found";
  } else if (status && status >= 500) {
    headline = "Server error";
  }

  return (
    <div className="empty error-state" role="alert">
      <div style={{ fontWeight: 600, marginBottom: 6 }}>{headline}</div>
      <div className="hint" style={{ marginBottom: 12 }}>
        {status != null && status > 0 ? `HTTP ${status}: ` : ""}
        {hint}
      </div>
      {onRetry && (
        <button type="button" className="chip" onClick={onRetry}>
          Retry
        </button>
      )}
    </div>
  );
}
