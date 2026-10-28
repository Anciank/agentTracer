/**
 * Pair LLM tool_calls with ToolMessages and/or child tool runs in the trace tree.
 *
 * The /messages API only pairs call_id ↔ ToolMessage within the same payload.
 * LangGraph often parks the tool result on a sibling `tools` chain, so the UI
 * must also match against child (or cousin) tool runs:
 *   1. tool_call id ↔ run inputs/extra/metadata.tool_call_id (when present)
 *   2. else tool name + order under the same parent LLM/agent step
 *
 * Warn only when there is neither a ToolMessage nor a matching child run.
 */

import type { ChatMessage, MessagesResponse, ToolCall } from "../api/types";

export type PairableRun = {
  id: string;
  name: string;
  parent_run_id: string | null;
  run_type?: string;
  start_time?: string;
  inputs?: unknown;
  extra?: unknown;
};

export type ToolCallPairStatus = {
  callId: string;
  name: string;
  /** True when a ToolMessage in this payload references the call. */
  hasToolMessage: boolean;
  /** Matching child/cousin tool run id, if any. */
  childRunId: string | null;
  /** True only when neither ToolMessage nor child run matches. */
  unpaired: boolean;
};

function asRecord(v: unknown): Record<string, unknown> | null {
  return v && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, unknown>) : null;
}

/** Pull a tool_call_id from run inputs / extra / metadata when the server stored one. */
export function toolCallIdFromRun(run: PairableRun): string | null {
  const tryVal = (v: unknown): string | null => {
    if (typeof v === "string" && v) return v;
    return null;
  };
  const inputs = asRecord(run.inputs);
  if (inputs) {
    for (const k of ["tool_call_id", "toolCallId", "id", "call_id"]) {
      const hit = tryVal(inputs[k]);
      if (hit && (k !== "id" || hit.startsWith("call_"))) return hit;
    }
  }
  const extra = asRecord(run.extra);
  if (extra) {
    for (const k of ["tool_call_id", "toolCallId", "call_id"]) {
      const hit = tryVal(extra[k]);
      if (hit) return hit;
    }
    const meta = asRecord(extra.metadata);
    if (meta) {
      for (const k of ["tool_call_id", "toolCallId", "call_id", "ls_tool_call_id"]) {
        const hit = tryVal(meta[k]);
        if (hit) return hit;
      }
    }
  }
  return null;
}

function collectToolCalls(messages: ChatMessage[]): ToolCall[] {
  const out: ToolCall[] = [];
  for (const m of messages) {
    if (m.tool_calls?.length) out.push(...m.tool_calls);
    for (const p of m.content || []) {
      if (p && typeof p === "object" && (p as { type?: string }).type === "tool_use") {
        const tu = p as { id: string; name: string; input?: Record<string, unknown> };
        out.push({ id: tu.id, name: tu.name, arguments: tu.input || {} });
      }
    }
  }
  return out;
}

function toolMessageCallIds(messages: ChatMessage[]): Set<string> {
  const ids = new Set<string>();
  for (const m of messages) {
    if (m.role === "tool" && m.tool_call_id) ids.add(m.tool_call_id);
    for (const p of m.content || []) {
      if (p && typeof p === "object" && (p as { type?: string }).type === "tool_result") {
        const id = (p as { tool_use_id?: string }).tool_use_id;
        if (id) ids.add(id);
      }
    }
  }
  return ids;
}

/**
 * Runs that count as tool executions for pairing: run_type=tool, or any run
 * whose inputs/extra already expose a tool_call_id.
 */
function isToolish(run: PairableRun): boolean {
  if (run.run_type === "tool") return true;
  return toolCallIdFromRun(run) != null;
}

/**
 * Candidate tool runs for an LLM/agent step: direct children, children of a
 * sibling `tools` chain that shares the same parent, and (when viewing an LLM)
 * the same relative to the parent agent chain.
 */
export function candidateToolRuns(
  currentRunId: string | null | undefined,
  runs: PairableRun[]
): PairableRun[] {
  if (!currentRunId || !runs.length) {
    return runs.filter(isToolish);
  }
  const byId = new Map(runs.map((r) => [r.id, r]));
  const current = byId.get(currentRunId);
  // Step ids: the run itself + its parent (LLM under agent → also match agent's cousins).
  const stepIds = new Set<string>([currentRunId]);
  if (current?.parent_run_id) stepIds.add(current.parent_run_id);

  // Parents of steps — used to find sibling `tools` chains.
  const stepParents = new Set<string>();
  for (const sid of stepIds) {
    const s = byId.get(sid);
    if (s?.parent_run_id) stepParents.add(s.parent_run_id);
  }

  const toolsParents = new Set<string>();
  for (const r of runs) {
    if (stepIds.has(r.id)) continue;
    const isToolsNode =
      r.name === "tools" ||
      (r.run_type === "chain" && /tools?/i.test(r.name));
    if (!isToolsNode) continue;
    // Sibling of a step (same parent), or child of a step.
    if ((r.parent_run_id && stepParents.has(r.parent_run_id)) || (r.parent_run_id && stepIds.has(r.parent_run_id))) {
      toolsParents.add(r.id);
    }
  }

  const out: PairableRun[] = [];
  const seen = new Set<string>();
  for (const r of runs) {
    if (!isToolish(r)) continue;
    const parent = r.parent_run_id;
    const ok =
      (parent && stepIds.has(parent)) ||
      (parent && toolsParents.has(parent)) ||
      // Direct child of a tools-ish parent already covered; also allow tool
      // whose grandparent is a step parent (cousin).
      (() => {
        if (!parent) return false;
        const p = byId.get(parent);
        return !!(p?.parent_run_id && stepParents.has(p.parent_run_id));
      })();
    if (ok && !seen.has(r.id)) {
      seen.add(r.id);
      out.push(r);
    }
  }
  out.sort((a, b) => (a.start_time || "").localeCompare(b.start_time || "") || a.id.localeCompare(b.id));
  return out;
}

export type ResolveOpts = {
  currentRunId?: string | null;
  /** Span/tool runs from the same trace (summary rows + optional full payloads). */
  runs?: PairableRun[];
};

/**
 * Resolve pairing for every tool_call in the messages payload.
 * Starts from API `paired`, adds ToolMessage ids, then child-run matches.
 */
export function resolveToolCallPairing(
  data: MessagesResponse,
  opts: ResolveOpts = {}
): ToolCallPairStatus[] {
  const apiPaired = new Set(data.paired.map((p) => p.call_id));
  const msgPaired = toolMessageCallIds(data.messages);
  const calls = collectToolCalls(data.messages);
  const candidates = candidateToolRuns(opts.currentRunId, opts.runs || []);

  // Index by explicit tool_call_id on the run.
  const byCallId = new Map<string, PairableRun>();
  for (const r of candidates) {
    const cid = toolCallIdFromRun(r);
    if (cid && !byCallId.has(cid)) byCallId.set(cid, r);
  }

  const claimedRuns = new Set<string>();
  const statuses: ToolCallPairStatus[] = [];

  for (const call of calls) {
    const hasToolMessage = apiPaired.has(call.id) || msgPaired.has(call.id);
    let childRunId: string | null = null;

    const byId = byCallId.get(call.id);
    if (byId && !claimedRuns.has(byId.id)) {
      childRunId = byId.id;
      claimedRuns.add(byId.id);
    } else {
      // Name + order under the same parent step.
      const match = candidates.find((r) => !claimedRuns.has(r.id) && r.name === call.name);
      if (match) {
        childRunId = match.id;
        claimedRuns.add(match.id);
      }
    }

    statuses.push({
      callId: call.id,
      name: call.name,
      hasToolMessage,
      childRunId,
      unpaired: !hasToolMessage && !childRunId,
    });
  }

  return statuses;
}

/** Call ids that should NOT show an unpaired warning. */
export function resolvedPairedCallIds(data: MessagesResponse, opts: ResolveOpts = {}): Set<string> {
  const set = new Set<string>();
  for (const s of resolveToolCallPairing(data, opts)) {
    if (!s.unpaired) set.add(s.callId);
  }
  // Also keep API-paired ids even if somehow missing from tool_calls list.
  for (const p of data.paired) set.add(p.call_id);
  return set;
}
