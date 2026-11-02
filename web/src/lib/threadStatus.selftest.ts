import { ApiError } from "../api/client";
import {
  isThreadsEndpointMissing,
  shortThreadId,
  threadKeySourceLabel,
} from "./threadStatus";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error(msg);
}

assert(isThreadsEndpointMissing(new ApiError(404, "nope")), "404 → missing");
assert(isThreadsEndpointMissing(new ApiError(501, "todo")), "501 → missing");
assert(!isThreadsEndpointMissing(new ApiError(500, "boom")), "500 → not missing");
assert(!isThreadsEndpointMissing(new Error("x")), "plain Error → not missing");
assert(threadKeySourceLabel("session_id") === "grouped by metadata.session_id", "session label");
assert(threadKeySourceLabel("thread_id") === "grouped by metadata.thread_id", "thread label");
assert(shortThreadId("abcdefghijklmnop") === "abcdefgh", "short");
assert(shortThreadId("abc") === "abc", "short passthrough");
console.log("threadStatus.selftest: ok");
