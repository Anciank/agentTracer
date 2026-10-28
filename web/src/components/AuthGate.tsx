import { useState, FormEvent } from "react";
import { getApiKey, setApiKey } from "../lib/auth";
import { listProjects, ApiError } from "../api/client";

type Props = {
  needAuth: boolean;
  onReady: () => void;
};

export function AuthGate({ needAuth, onReady }: Props) {
  const [key, setKey] = useState(getApiKey());
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  if (!needAuth) return null;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!key.trim()) {
      setErr("API key required");
      return;
    }
    setApiKey(key.trim(), true);
    setErr("");
    setBusy(true);
    try {
      await listProjects();
      onReady();
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        setErr("Invalid API key");
      } else {
        setErr(e instanceof Error ? e.message : "Could not verify key");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="auth-overlay" role="dialog" aria-label="API key">
      <form className="auth-card" onSubmit={(e) => void submit(e)}>
        <h2>Enter API key</h2>
        <p className="hint">
          The UI calls <code>/api/ui/*</code> with <code>x-api-key</code> (same key as LangSmith ingest).
          Stored in localStorage for this browser.
        </p>
        <input
          type="password"
          autoFocus
          placeholder="AGENTTRACER_API_KEY"
          value={key}
          disabled={busy}
          onChange={(e) => setKey(e.target.value)}
        />
        {err && <div className="banner error">{err}</div>}
        <div className="row">
          <button type="submit" className="btn active" disabled={busy}>
            {busy ? "Checking…" : "Continue"}
          </button>
        </div>
      </form>
    </div>
  );
}
