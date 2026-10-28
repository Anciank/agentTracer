/**
 * DEV / VITE_MOCK fixtures for M3.5 agent map. Never statically imported from prod.
 */
import type {
  EdgeStats,
  GraphResponse,
  GraphWindow,
  NodeStats,
  PathGroup,
  PathsResponse,
  Tokens,
} from "../api/types";

const T = (input: number, output: number): Tokens => ({
  input,
  output,
  total: input + output,
});

function node(
  partial: Partial<NodeStats> & Pick<NodeStats, "key" | "label" | "count" | "error_count">
): NodeStats {
  const error_rate = partial.count ? partial.error_count / partial.count : 0;
  return {
    run_type: null,
    kind: "langgraph",
    p50_ms: 100,
    p95_ms: 500,
    tokens: T(1000, 200),
    truncated: false,
    ...partial,
    error_rate: partial.error_rate ?? error_rate,
  };
}

function edge(
  partial: Partial<EdgeStats> & Pick<EdgeStats, "from" | "to" | "count" | "share">
): EdgeStats {
  return {
    avg_ms: 40,
    p95_ms: 200,
    error_count: 0,
    loop: false,
    repeat_avg: null,
    repeat_max: null,
    ...partial,
  };
}

const SUPPORT_ID = "11111111-1111-1111-1111-111111111111";
const RESEARCH_ID = "22222222-2222-2222-2222-222222222222";
/** Max-size layout/render fixture (40 nodes). */
export const LARGE_ID = "33333333-3333-3333-3333-333333333333";

const SUPPORT_NODES: NodeStats[] = [
  node({ key: "supervisor", label: "supervisor", count: 4200, error_count: 40, p50_ms: 80, p95_ms: 420, tokens: T(400000, 80000) }),
  node({ key: "tools", label: "tools", count: 5200, error_count: 400, error_rate: 0.077, p50_ms: 90, p95_ms: 900, tokens: T(200000, 40000) }),
  node({ key: "retrieve", label: "retrieve", count: 3100, error_count: 12, p50_ms: 60, p95_ms: 380, tokens: T(150000, 10000) }),
  node({ key: "respond", label: "respond", count: 4000, error_count: 8, p50_ms: 200, p95_ms: 1100, tokens: T(500000, 220000) }),
  node({ key: "route", label: "route", count: 2800, error_count: 5, p50_ms: 20, p95_ms: 90 }),
  node({ key: "validate", label: "validate", count: 1900, error_count: 55, p50_ms: 40, p95_ms: 220 }),
  node({ key: "rewrite", label: "rewrite", count: 900, error_count: 20, p50_ms: 150, p95_ms: 700 }),
  node({ key: "summarize", label: "summarize", count: 800, error_count: 3, p50_ms: 300, p95_ms: 1400 }),
  node({ key: "escalate", label: "escalate", count: 220, error_count: 80, error_rate: 0.36, p50_ms: 50, p95_ms: 300 }),
  node({ key: "billing", label: "billing", count: 600, error_count: 15, p50_ms: 70, p95_ms: 400 }),
  node({ key: "refund", label: "refund", count: 180, error_count: 22, p50_ms: 110, p95_ms: 650 }),
  node({ key: "search", label: "search", count: 1400, error_count: 9, p50_ms: 45, p95_ms: 260 }),
  node({ key: "rank", label: "rank", count: 1100, error_count: 2, p50_ms: 30, p95_ms: 120 }),
  node({ key: "compose", label: "compose", count: 950, error_count: 4, p50_ms: 180, p95_ms: 800 }),
  node({ key: "guard", label: "guard", count: 700, error_count: 18, p50_ms: 25, p95_ms: 100 }),
];

const SUPPORT_OTHER: NodeStats = node({
  key: "__other__",
  label: "12 more",
  kind: "other",
  count: 600,
  error_count: 0,
  error_rate: 0,
  p50_ms: null,
  p95_ms: null,
  tokens: T(0, 0),
  truncated: true,
  rolled_keys: ["idle", "cleanup", "notify", "archive", "tag", "score", "dedupe", "cache", "warmup", "sleep", "ping", "noop"],
});

const SUPPORT_EDGES: EdgeStats[] = [
  edge({ from: "supervisor", to: "route", count: 4000, share: 0.4 }),
  edge({ from: "route", to: "tools", count: 2200, share: 0.22 }),
  edge({ from: "route", to: "retrieve", count: 1500, share: 0.15 }),
  edge({ from: "route", to: "billing", count: 400, share: 0.04 }),
  edge({ from: "tools", to: "tools", count: 900, share: 0.09, loop: true, repeat_avg: 2.4, repeat_max: 50, error_count: 40 }),
  edge({ from: "tools", to: "validate", count: 1800, share: 0.18, error_count: 30 }),
  edge({ from: "validate", to: "respond", count: 1600, share: 0.16 }),
  edge({ from: "validate", to: "rewrite", count: 200, share: 0.02, error_count: 10 }),
  edge({ from: "rewrite", to: "tools", count: 180, share: 0.018 }),
  edge({ from: "retrieve", to: "search", count: 1400, share: 0.14 }),
  edge({ from: "search", to: "rank", count: 1100, share: 0.11 }),
  edge({ from: "rank", to: "compose", count: 950, share: 0.095 }),
  edge({ from: "compose", to: "respond", count: 900, share: 0.09 }),
  edge({ from: "billing", to: "refund", count: 160, share: 0.016, error_count: 12 }),
  edge({ from: "refund", to: "respond", count: 140, share: 0.014 }),
  edge({ from: "respond", to: "guard", count: 700, share: 0.07 }),
  edge({ from: "guard", to: "escalate", count: 120, share: 0.012, error_count: 40 }),
  edge({ from: "supervisor", to: "summarize", count: 200, share: 0.02 }),
  edge({ from: "summarize", to: "respond", count: 180, share: 0.018 }),
];

const SUPPORT_HIDDEN: EdgeStats[] = [
  edge({ from: "idle", to: "cleanup", count: 40, share: 0.004 }),
  edge({ from: "notify", to: "archive", count: 30, share: 0.003 }),
  edge({ from: "tag", to: "score", count: 25, share: 0.0025 }),
  edge({ from: "cache", to: "warmup", count: 20, share: 0.002 }),
  edge({ from: "ping", to: "noop", count: 10, share: 0.001 }),
  edge({ from: "escalate", to: "supervisor", count: 80, share: 0.008, loop: true, repeat_avg: 1.2, repeat_max: 3 }),
];

function windowBounds(w: GraphWindow): { from: string; to: string } {
  const to = new Date("2026-10-28T07:48:00.000Z");
  const ms = w === "1h" ? 3600e3 : w === "7d" ? 7 * 86400e3 : 86400e3;
  return { from: new Date(to.getTime() - ms).toISOString(), to: to.toISOString() };
}

function scaleForWindow(w: GraphWindow, n: number): number {
  if (w === "1h") return Math.max(1, Math.round(n / 24));
  if (w === "7d") return n * 7;
  return n;
}

export function supportGraph(window: GraphWindow = "24h"): GraphResponse {
  const { from, to } = windowBounds(window);
  const scale = (n: number) => scaleForWindow(window, n);
  const nodes = SUPPORT_NODES.map((n) => ({
    ...n,
    count: scale(n.count),
    error_count: scale(n.error_count),
    tokens: {
      input: scale(n.tokens.input),
      output: scale(n.tokens.output),
      total: scale(n.tokens.total),
    },
  }));
  const other = {
    ...SUPPORT_OTHER,
    count: scale(SUPPORT_OTHER.count),
    label: SUPPORT_OTHER.label,
  };
  const edges = SUPPORT_EDGES.map((e) => ({ ...e, count: scale(e.count) }));
  const edges_hidden = SUPPORT_HIDDEN.map((e) => ({ ...e, count: scale(e.count) }));
  return {
    project_id: SUPPORT_ID,
    project: "support",
    window,
    from,
    to,
    trace_count: scale(10000),
    mode: "langgraph",
    nodes,
    other,
    edges,
    edges_hidden,
    truncated: { nodes: true, edges: true },
  };
}

export function researchGraph(window: GraphWindow = "24h"): GraphResponse {
  const { from, to } = windowBounds(window);
  const nodes = [
    node({ key: "plan", label: "plan", count: 400, error_count: 10, p95_ms: 600 }),
    node({ key: "search", label: "search", count: 800, error_count: 40, p95_ms: 2000 }),
    node({ key: "read", label: "read", count: 600, error_count: 5, p95_ms: 1500 }),
    node({ key: "write", label: "write", count: 350, error_count: 8, p95_ms: 3000 }),
    node({ key: "critique", label: "critique", count: 200, error_count: 60, error_rate: 0.3, p95_ms: 900 }),
  ];
  const edges = [
    edge({ from: "plan", to: "search", count: 380, share: 0.84 }),
    edge({ from: "search", to: "read", count: 500, share: 1.1 }),
    edge({ from: "read", to: "search", count: 120, share: 0.27, loop: true, repeat_avg: 1.8, repeat_max: 6 }),
    edge({ from: "read", to: "write", count: 300, share: 0.67 }),
    edge({ from: "write", to: "critique", count: 200, share: 0.44 }),
    edge({ from: "critique", to: "write", count: 80, share: 0.18, loop: true, repeat_avg: 1.4, repeat_max: 4, error_count: 30 }),
  ];
  return {
    project_id: RESEARCH_ID,
    project: "research",
    window,
    from,
    to,
    trace_count: 450,
    mode: "langgraph",
    nodes,
    other: null,
    edges,
    edges_hidden: [edge({ from: "plan", to: "write", count: 3, share: 0.007 })],
    truncated: { nodes: false, edges: true },
  };
}

export function getGraphFixture(projectId: string, window: GraphWindow = "24h"): GraphResponse {
  if (projectId === RESEARCH_ID || projectId === "research") return researchGraph(window);
  if (projectId === LARGE_ID || projectId === "large") return makeLargeGraph(40, 8);
  return supportGraph(window);
}

const SUPPORT_PATHS: PathGroup[] = [
  {
    fingerprint: "supervisor>route>tools×3>validate>respond",
    label: "supervisor → route → tools ×3 → validate → respond",
    count: 1200,
    error_count: 80,
    error_rate: 0.067,
    p95_ms: 4200,
    fail_nodes: ["tools"],
    is_new: false,
    steps: [
      { key: "supervisor", repeat: 1 },
      { key: "route", repeat: 1 },
      { key: "tools", repeat: 3 },
      { key: "validate", repeat: 1 },
      { key: "respond", repeat: 1 },
    ],
  },
  {
    fingerprint: "supervisor>route>retrieve>search>rank>compose>respond",
    label: "supervisor → route → retrieve → search → rank → compose → respond",
    count: 900,
    error_count: 10,
    error_rate: 0.011,
    p95_ms: 5100,
    fail_nodes: [],
    is_new: false,
    steps: [
      { key: "supervisor", repeat: 1 },
      { key: "route", repeat: 1 },
      { key: "retrieve", repeat: 1 },
      { key: "search", repeat: 1 },
      { key: "rank", repeat: 1 },
      { key: "compose", repeat: 1 },
      { key: "respond", repeat: 1 },
    ],
  },
  {
    fingerprint: "supervisor>route>billing>refund>respond",
    label: "supervisor → route → billing → refund → respond",
    count: 140,
    error_count: 22,
    error_rate: 0.157,
    p95_ms: 3800,
    fail_nodes: ["refund"],
    is_new: true,
    steps: [
      { key: "supervisor", repeat: 1 },
      { key: "route", repeat: 1 },
      { key: "billing", repeat: 1 },
      { key: "refund", repeat: 1 },
      { key: "respond", repeat: 1 },
    ],
  },
  {
    fingerprint: "supervisor>route>tools×6>escalate",
    label: "supervisor → route → tools ×6 → escalate",
    count: 60,
    error_count: 40,
    error_rate: 0.67,
    p95_ms: 9000,
    fail_nodes: ["tools", "escalate"],
    is_new: false,
    steps: [
      { key: "supervisor", repeat: 1 },
      { key: "route", repeat: 1 },
      { key: "tools", repeat: 6 },
      { key: "escalate", repeat: 1 },
    ],
  },
];

export function getPathsFixture(projectId: string, window: GraphWindow = "24h"): PathsResponse {
  const g = getGraphFixture(projectId, window);
  const paths =
    g.project === "research"
      ? [
          {
            fingerprint: "plan>search>read×2>write>critique",
            label: "plan → search → read ×2 → write → critique",
            count: 200,
            error_count: 40,
            error_rate: 0.2,
            p95_ms: 12000,
            fail_nodes: ["critique"],
            is_new: true,
            steps: [
              { key: "plan", repeat: 1 },
              { key: "search", repeat: 1 },
              { key: "read", repeat: 2 },
              { key: "write", repeat: 1 },
              { key: "critique", repeat: 1 },
            ],
          },
        ]
      : SUPPORT_PATHS;
  return {
    project_id: g.project_id,
    project: g.project,
    window,
    from: g.from,
    to: g.to,
    trace_count: g.trace_count,
    paths,
    next_cursor: null,
  };
}

/** Large synthetic graph for perf self-test (40 nodes, dense edges). */
export function makeLargeGraph(nodeCount = 40, edgeFactor = 3): GraphResponse {
  const nodes: NodeStats[] = [];
  for (let i = 0; i < nodeCount; i++) {
    const err = i % 7 === 0 ? 50 + i : i === 3 ? 120 : 0;
    nodes.push(
      node({
        key: `n${String(i).padStart(2, "0")}`,
        label: `node_${i}`,
        count: 1000 - i * 10,
        error_count: err,
        p50_ms: 50 + i,
        p95_ms: 200 + i * 20,
        tokens: T(1000 * (nodeCount - i), 200 * (nodeCount - i)),
      })
    );
  }
  // Keep top 15 + failing always visible pattern already baked into nodes list;
  // roll the rest into other for API realism.
  const failing = nodes.filter((n) => n.error_count > 0);
  const byVol = [...nodes].sort((a, b) => b.count - a.count || a.key.localeCompare(b.key));
  const top = new Map<string, NodeStats>();
  for (const n of byVol.slice(0, 15)) top.set(n.key, n);
  for (const n of failing) top.set(n.key, n);
  const shown = [...top.values()].sort(
    (a, b) => b.error_count - a.error_count || b.count - a.count || a.key.localeCompare(b.key)
  );
  const rolled = nodes.filter((n) => !top.has(n.key));
  const other: NodeStats | null = rolled.length
    ? node({
        key: "__other__",
        label: `${rolled.length} more`,
        kind: "other",
        count: rolled.reduce((s, n) => s + n.count, 0),
        error_count: 0,
        truncated: true,
        rolled_keys: rolled.map((n) => n.key),
        p50_ms: null,
        p95_ms: null,
        tokens: T(0, 0),
      })
    : null;

  const edges: EdgeStats[] = [];
  const hidden: EdgeStats[] = [];
  for (let i = 0; i < nodeCount - 1; i++) {
    const share = (nodeCount - i) / (nodeCount * 2);
    const e = edge({
      from: `n${String(i).padStart(2, "0")}`,
      to: `n${String(i + 1).padStart(2, "0")}`,
      count: 800 - i * 5,
      share,
      loop: false,
    });
    (share < 0.01 ? hidden : edges).push(e);
  }
  // loops
  edges.push(
    edge({
      from: "n03",
      to: "n03",
      count: 400,
      share: 0.08,
      loop: true,
      repeat_avg: 6.2,
      repeat_max: 50,
      error_count: 20,
    })
  );
  for (let i = 0; i < edgeFactor; i++) {
    const a = i % nodeCount;
    const b = (i * 7 + 3) % nodeCount;
    if (a === b) continue;
    const share = 0.005 + (i % 5) * 0.002;
    const e = edge({
      from: `n${String(a).padStart(2, "0")}`,
      to: `n${String(b).padStart(2, "0")}`,
      count: 50 + i,
      share,
    });
    (share < 0.01 ? hidden : edges).push(e);
  }
  const { from, to } = windowBounds("24h");
  return {
    project_id: SUPPORT_ID,
    project: "support",
    window: "24h",
    from,
    to,
    trace_count: 10000,
    mode: "langgraph",
    nodes: shown,
    other,
    edges,
    edges_hidden: hidden,
    truncated: { nodes: !!other, edges: hidden.length > 0 },
  };
}

/** Mock: which fixture traces "match" a node/edge/path filter. */
export const MOCK_FILTER_TRACE_IDS: Record<string, string[]> = {
  "node:tools": ["aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "bbbbbbbb-cccc-dddd-eeee-ffffffffffff"],
  "node:escalate": ["bbbbbbbb-cccc-dddd-eeee-ffffffffffff"],
  "edge:tools>tools": ["aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"],
  "edge:supervisor>route": ["aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "cccccccc-dddd-eeee-ffff-000000000000"],
  "path:supervisor>route>tools×3>validate>respond": ["aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"],
  "node_fail:tools": ["bbbbbbbb-cccc-dddd-eeee-ffffffffffff"],
};
