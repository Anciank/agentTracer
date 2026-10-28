# agentTracer web (M3)

React 18 + Vite + TypeScript + TanStack Query + react-virtual SPA.

```bash
cd web
npm install
npm run build   # writes into ../server/ui/static (embedded by Go)
npm run dev     # proxy to :1984
```

**API:** calls `/api/ui/*` only (frozen shapes). Ships in **mock mode** by default when those endpoints 404 / are unreachable; set `VITE_MOCK=0` to force the real API, or `VITE_MOCK=1` to force fixtures.

**Auth:** sends `x-api-key` from localStorage (key-entry gate on 401). `/ui` shell stays exempt like M1; `/api/ui/*` is keyed.

**No cost fields.** Tokens only.
