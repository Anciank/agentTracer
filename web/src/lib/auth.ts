const KEY = "agentTracer.apiKey";

export function getApiKey(): string {
  try {
    return sessionStorage.getItem(KEY) || localStorage.getItem(KEY) || "";
  } catch {
    return "";
  }
}

export function setApiKey(key: string, persist = true): void {
  try {
    sessionStorage.setItem(KEY, key);
    if (persist) localStorage.setItem(KEY, key);
    else localStorage.removeItem(KEY);
  } catch {
    /* ignore */
  }
}

export function clearApiKey(): void {
  try {
    sessionStorage.removeItem(KEY);
    localStorage.removeItem(KEY);
  } catch {
    /* ignore */
  }
}
