/**
 * Status mapping for agent-map empty vs error.
 * Run: npx tsx src/lib/mapStatus.selftest.ts
 */
import { ApiError } from "../api/client";
import { isMapEndpointMissing } from "./mapStatus";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error(msg);
}

assert(isMapEndpointMissing(new ApiError(404, "nope")), "404 → missing");
assert(isMapEndpointMissing(new ApiError(501, "todo")), "501 → missing");
assert(!isMapEndpointMissing(new ApiError(500, "boom")), "500 → not missing");
assert(!isMapEndpointMissing(new ApiError(401, "auth")), "401 → not missing");
assert(!isMapEndpointMissing(new ApiError(0, "net")), "network → not missing");
assert(!isMapEndpointMissing(new Error("x")), "generic → not missing");
assert(!isMapEndpointMissing(null), "null → not missing");
console.log(JSON.stringify({ ok: true, cases: 7 }));
