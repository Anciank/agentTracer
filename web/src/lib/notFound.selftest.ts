import { ApiError } from "../api/client";
import { isNotFoundError } from "./notFound";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error(`FAIL: ${msg}`);
}

assert(isNotFoundError(new ApiError(404, "missing")), "404");
assert(!isNotFoundError(new ApiError(500, "x")), "500");
assert(!isNotFoundError(new ApiError(401, "x")), "401");
assert(!isNotFoundError(new Error("404")), "generic");
assert(!isNotFoundError(null), "null");
console.log("notFound.selftest: ok");
