import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  detectLangGraphFlags,
  detectLangGraphFlagsFromListItem,
  hasCommandResumeInput,
} from "./langgraphFlags";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error(`FAIL: ${msg}`);
}

const here = dirname(fileURLToPath(import.meta.url));
const resume = JSON.parse(
  readFileSync(join(here, "../fixtures/round2/resume_root.json"), "utf8")
) as {
  name: string;
  inputs: unknown;
  outputs: unknown;
  extra: unknown;
  error: unknown;
  status: string;
};
const gate = JSON.parse(
  readFileSync(join(here, "../fixtures/round2/interrupt_human_gate.json"), "utf8")
) as {
  name: string;
  inputs: unknown;
  outputs: unknown;
  extra: unknown;
  error: unknown;
  status: string;
};

assert(hasCommandResumeInput(resume.inputs), "resume inputs.input.resume present");
const rf = detectLangGraphFlags(resume);
assert(rf.resumed, "resume root → resumed");
assert(!rf.interrupted, "resume root not interrupted");

const gf = detectLangGraphFlags(gate);
assert(gf.interrupted, "human_gate GraphInterrupt → interrupted");
assert(!gf.resumed, "human_gate not resumed");

const list = detectLangGraphFlagsFromListItem({ name: "approval_plan#0-resume", status: "success" });
assert(list.resumed, "list name -resume → resumed badge");

assert(!detectLangGraphFlags({ name: "agent", inputs: { input: "hi" } }).resumed, "plain agent not resumed");
assert(
  !detectLangGraphFlags({ name: "tools", error: "ValueError: boom" }).interrupted,
  "plain error not interrupt"
);

console.log("langgraphFlags.selftest: ok");
console.log(
  "keyed fields: inputs.input.resume | inputs.resume | name~-resume | error~GraphInterrupt/Interrupt( | outputs.__interrupt__"
);
