import { lazy, Suspense, useCallback, useEffect, useState } from "react";
import { Link, Navigate, Route, Routes, useNavigate, useLocation } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ProjectsPage } from "./components/Projects";
import { TracesPage } from "./pages/TracesPage";
import { HelpOverlay } from "./components/HelpOverlay";
import { AuthGate } from "./components/AuthGate";
const ProjectPage = lazy(() => import("./pages/ProjectPage"));
const MapRedirect = lazy(() => import("./pages/MapRedirect"));
import { getApiKey } from "./lib/auth";
import { getApiMode, listProjects, ApiError, mocksAllowed } from "./api/client";

const qc = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 5_000,
      retry: (n, err) => {
        if (err instanceof ApiError && (err.status === 401 || err.status === 0)) return false;
        return n < 1;
      },
      refetchOnWindowFocus: false,
    },
  },
});

function ThemeToggle() {
  const [theme, setTheme] = useState(() =>
    document.documentElement.dataset.theme === "dark" ? "dark" : "light"
  );
  const toggle = useCallback(() => {
    const next = theme === "dark" ? "light" : "dark";
    document.documentElement.dataset.theme = next;
    try {
      localStorage.setItem("agentTracer.theme", next);
    } catch {
      /* ignore */
    }
    setTheme(next);
  }, [theme]);
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const tag = (e.target as HTMLElement)?.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA") return;
      if (e.key === "t") toggle();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [toggle]);
  return (
    <button type="button" className="chip" onClick={toggle} title="Toggle theme (t)">
      {theme === "dark" ? "Light" : "Dark"}
    </button>
  );
}

function Shell() {
  const [help, setHelp] = useState(false);
  const [needAuth, setNeedAuth] = useState(false);
  const [modeLabel, setModeLabel] = useState(getApiMode());
  const nav = useNavigate();
  const loc = useLocation();
  const showMockChip = mocksAllowed();

  const probe = useCallback(async () => {
    try {
      await listProjects();
      setNeedAuth(false);
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) {
        setNeedAuth(true);
      }
    } finally {
      setModeLabel(getApiMode());
    }
  }, []);

  useEffect(() => {
    void probe();
  }, [probe]);

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const tag = (e.target as HTMLElement)?.tagName;
      const typing = tag === "INPUT" || tag === "TEXTAREA";
      if (e.key === "?" && !typing && !e.metaKey && !e.ctrlKey) {
        e.preventDefault();
        setHelp((h) => !h);
        return;
      }
      if (e.key === "Escape" && help) {
        setHelp(false);
        return;
      }
      if (e.key === "e" && !typing && loc.pathname === "/") {
        e.preventDefault();
        nav("/traces?status=error");
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [help, loc.pathname, nav]);

  return (
    <div className="app">
      <header className="topbar">
        <Link to="/" style={{ textDecoration: "none", color: "inherit" }}>
          <h1>
            agentTracer <span className="sfx">· traces</span>
          </h1>
        </Link>
        <Link to="/traces" className="chip">
          Traces
        </Link>
        <div className="spacer" />
        {showMockChip && <span className="mode">api: {modeLabel}</span>}
        <ThemeToggle />
        <button type="button" className="chip" onClick={() => setHelp(true)}>
          ?
        </button>
      </header>
      <Routes>
        <Route path="/" element={<ProjectsPage />} />
        <Route path="/projects/:projectId" element={
          <Suspense fallback={<div className="page empty">Loading project…</div>}>
            <ProjectPage />
          </Suspense>
        } />
        <Route path="/map/:projectId" element={
          <Suspense fallback={<div className="page empty">Opening map…</div>}>
            <MapRedirect />
          </Suspense>
        } />
        <Route path="/traces" element={<TracesPage />} />
        <Route path="/traces/:traceId" element={<TracesPage />} />
        <Route path="/p/:project" element={<ProjectRedirect />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
      {help && <HelpOverlay onClose={() => setHelp(false)} />}
      <AuthGate
        needAuth={needAuth}
        onReady={() => {
          setNeedAuth(false);
          qc.clear();
          void probe();
        }}
      />
    </div>
  );
}

function ProjectRedirect() {
  const loc = useLocation();
  const name = loc.pathname.split("/p/")[1] || "";
  return <Navigate to={`/traces?project=${encodeURIComponent(name)}`} replace />;
}

export default function App() {
  return (
    <QueryClientProvider client={qc}>
      <Shell />
    </QueryClientProvider>
  );
}
