import { activateOpenId, traceOpenPath } from "./traceNav";
import { filtersFromSearch } from "./filters";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error(msg);
}

// Click/Enter must open even when a trace is already deep-linked.
assert(activateOpenId("b", "a") === "b", "switch while open → b");
assert(activateOpenId("a", null) === "a", "first open → a");
assert(activateOpenId(null, "a") === null, "no selection → null");

const f = filtersFromSearch(new URLSearchParams("status=error&project=demo"));
const path = traceOpenPath("trace-1", f);
assert(path.startsWith("/traces/trace-1?"), `path=${path}`);
assert(path.includes("status=error"), "preserves status");
assert(path.includes("project=demo"), "preserves project");

console.log("traceNav.selftest: ok");
