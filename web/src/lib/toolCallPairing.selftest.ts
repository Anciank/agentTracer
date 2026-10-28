/**
 * Unit tests for tool-call ↔ child-run pairing using the real lg-complex
 * slow_structured agent payload (ROUND2 unpaired banner false positive).
 */
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  candidateToolRuns,
  resolveToolCallPairing,
  resolvedPairedCallIds,
  toolCallIdFromRun,
  type PairableRun,
} from "./toolCallPairing";
import type { MessagesResponse } from "../api/types";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error(`FAIL: ${msg}`);
}

const here = dirname(fileURLToPath(import.meta.url));
const fixturePath = join(here, "../fixtures/round2/unpaired_agent.json");
const fixture = JSON.parse(readFileSync(fixturePath, "utf8")) as {
  agent_run_id: string;
  messages: MessagesResponse;
  rows: PairableRun[];
};

const CALL_ID = "call_00_vji6l14mxet9dks4iczxg8be";

assert(fixture.messages.paired.length === 0, "fixture API paired is empty (the false-positive case)");
assert(
  fixture.messages.messages.some((m) => m.tool_calls?.some((c) => c.id === CALL_ID)),
  "fixture has the slow_lookup tool_call"
);

// Real tool run has no tool_call_id in inputs — name+order path must win.
const slow = fixture.rows.find((r) => r.name === "slow_lookup" && r.run_type === "tool");
assert(slow, "fixture includes slow_lookup tool run");
assert(toolCallIdFromRun(slow) == null, "real slow_lookup inputs lack tool_call_id");

const cands = candidateToolRuns(fixture.agent_run_id, fixture.rows);
assert(
  cands.some((r) => r.id === slow.id),
  "slow_lookup is a candidate under the agent step (sibling tools chain)"
);

const statuses = resolveToolCallPairing(fixture.messages, {
  currentRunId: fixture.agent_run_id,
  runs: fixture.rows,
});
const st = statuses.find((s) => s.callId === CALL_ID);
assert(st, "status for call id");
assert(!st.hasToolMessage, "no ToolMessage in payload");
assert(st.childRunId === slow.id, `child run matched by name+order (got ${st.childRunId})`);
assert(!st.unpaired, "must NOT warn when child tool run exists");

const paired = resolvedPairedCallIds(fixture.messages, {
  currentRunId: fixture.agent_run_id,
  runs: fixture.rows,
});
assert(paired.has(CALL_ID), "resolvedPairedCallIds includes call");

// Without runs, still unpaired (API-only path — the old behavior).
const bare = resolveToolCallPairing(fixture.messages, {});
assert(bare.find((s) => s.callId === CALL_ID)?.unpaired === true, "no runs → unpaired");

// Explicit tool_call_id on inputs wins over name order.
const withId: PairableRun[] = fixture.rows.map((r) =>
  r.id === slow.id ? { ...r, inputs: { ...(r.inputs as object), tool_call_id: CALL_ID } } : r
);
const byId = resolveToolCallPairing(fixture.messages, {
  currentRunId: fixture.agent_run_id,
  runs: withId,
});
assert(byId.find((s) => s.callId === CALL_ID)?.childRunId === slow.id, "id match works");

// Viewing the child ChatOpenAI under agent should still find the cousin tool.
const llm = fixture.rows.find(
  (r) => r.name === "ChatOpenAI" && r.parent_run_id === fixture.agent_run_id
);
assert(llm, "agent has ChatOpenAI child");
const fromLlm = resolveToolCallPairing(fixture.messages, {
  currentRunId: llm.id,
  runs: fixture.rows,
});
assert(fromLlm.find((s) => s.callId === CALL_ID)?.unpaired === false, "LLM view pairs via parent agent");

// Genuinely missing tool → unpaired.
const ghost = resolveToolCallPairing(
  {
    ...fixture.messages,
    messages: [
      {
        id: "x",
        role: "assistant",
        name: null,
        content: [],
        tool_calls: [{ id: "call_missing", name: "no_such_tool", arguments: {} }],
        tool_call_id: null,
        source: "test",
      },
    ],
    paired: [],
  },
  { currentRunId: fixture.agent_run_id, runs: fixture.rows }
);
assert(ghost[0]?.unpaired === true, "unknown tool stays unpaired");

console.log("toolCallPairing.selftest: ok");
