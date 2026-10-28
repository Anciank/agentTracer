import { formatChatPreview, hasChatPreview, isTraceStatus, statusDotClass, statusTitle } from "./status";

function assert(cond: unknown, msg: string): void {
  if (!cond) throw new Error(msg);
}

assert(isTraceStatus("stale"), "stale is a TraceStatus");
assert(isTraceStatus("success"), "success");
assert(!isTraceStatus("bogus"), "bogus rejected");
assert(statusDotClass("stale") === "stale", "dot class");
assert(statusDotClass("error") === "err", "err class");
assert(statusTitle("stale") === "No end time recorded", "stale tooltip");
assert(statusTitle("running") === "running", "running tooltip");
assert(hasChatPreview({ first_user_preview: "hi", last_ai_preview: null }), "has user");
assert(!hasChatPreview({ first_user_preview: null, last_ai_preview: null }), "empty");
assert(
  formatChatPreview({ first_user_preview: "Hello", last_ai_preview: "Hi there" }) === "Hello → Hi there",
  "both"
);
assert(formatChatPreview({ first_user_preview: "Hello", last_ai_preview: null }) === "Hello", "user only");
assert(formatChatPreview({ first_user_preview: null, last_ai_preview: "Yo" }) === "Yo", "ai only");
console.log("status.selftest: ok");
