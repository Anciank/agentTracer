import { fmtToolTokens } from "./format";

function assert(cond: boolean, msg: string) {
  if (!cond) throw new Error(msg);
}

assert(fmtToolTokens(null) === "—", "null → em dash");
assert(fmtToolTokens(undefined) === "—", "undefined → em dash");
assert(fmtToolTokens({ input: 0, output: 0, total: 1200 }) === "1,200", "total-only");
assert(fmtToolTokens({ input: 0, output: 0, total: 0 }) === "0", "total-only zero");
assert(
  fmtToolTokens({ input: 100, output: 50, total: 150 }) === "100 in · 50 out",
  "in/out split"
);
assert(
  fmtToolTokens({ input: 10, output: 0, total: 10 }) === "10 in · 0 out",
  "partial in"
);
console.log("format.selftest OK");
