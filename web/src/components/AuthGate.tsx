import { useState, FormEvent } from "react";
import { getApiKey, setApiKey } from "../lib/auth";

type Props = {
  needAuth: boolean;
  onReady: () => void;
};

export function AuthGate({ needAuth, onReady }: Props) {
  const [key, setKey] = useState(getApiKey());
  const [err, setErr] = useState("");

  if (!needAuth) return null;

  function submit(e: FormEvent) {
    e.preventDefault();
    if (!key.trim()) {
      setErr("API key required");
      return;
    }
    setApiKey(key.trim(), true);
    setErr("");
    onReady();
  }

  return (
    <div className="auth-overlay" role="dialog" aria-label="API key">
      <form className="auth-card" onSubmit={submit}>
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
          onChange={(e) => setKey(e.target.value)}
        />
        {err && <div className="banner error">{err}</div>}
        <div className="row">
          <button type="submit" className="btn active">
            Continue
          </button>
        </div>
      </form>
    </div>
  );
}
