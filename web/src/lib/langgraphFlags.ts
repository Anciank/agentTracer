/**
 * Detect LangGraph resume / interrupt from payload fields available today.
 *
 * Isolated so CodeBro can later replace this with server-side linking
 * (parent interrupt ↔ resume child) without touching badge call sites.
 *
 * Fields keyed on (from real lg-complex /approval_plan payloads):
 *   resumed:
 *     - inputs.input.resume  (Command(resume=…) shape: {goto,graph,resume,update})
 *     - inputs.resume
 *     - extra.metadata.resume / langgraph_resume / langgraph_resuming (if present)
 *     - name ending with "-resume" / "#N-resume" (run_name from invoke config)
 *   interrupted:
 *     - error string matching GraphInterrupt or Interrupt(
 *     - outputs.__interrupt__ / outputs.interrupt
 *     - extra.metadata with interrupt markers (rare)
 */

export type LangGraphFlagSource = {
  name?: string | null;
  status?: string | null;
  has_error?: boolean;
  inputs?: unknown;
  outputs?: unknown;
  extra?: unknown;
  error?: unknown;
};

export type LangGraphFlags = {
  resumed: boolean;
  interrupted: boolean;
};

function asRecord(v: unknown): Record<string, unknown> | null {
  return v && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, unknown>) : null;
}

function errorText(error: unknown): string {
  if (error == null) return "";
  if (typeof error === "string") return error;
  if (typeof error === "object" && error && "preview" in (error as object)) {
    return String((error as { preview?: unknown }).preview ?? "");
  }
  try {
    return JSON.stringify(error);
  } catch {
    return String(error);
  }
}

/** True when inputs look like langgraph.types.Command(resume=…). */
export function hasCommandResumeInput(inputs: unknown): boolean {
  const root = asRecord(inputs);
  if (!root) return false;
  if (root.resume != null) return true;
  const inner = asRecord(root.input);
  if (inner && inner.resume != null) return true;
  // Nested one more level sometimes (wrapper).
  const deeper = inner ? asRecord(inner.input) : null;
  if (deeper && deeper.resume != null) return true;
  return false;
}

function metadataHasResume(extra: unknown): boolean {
  const meta = asRecord(asRecord(extra)?.metadata) || asRecord(extra);
  if (!meta) return false;
  for (const k of Object.keys(meta)) {
    const lk = k.toLowerCase();
    if (
      lk === "resume" ||
      lk === "langgraph_resume" ||
      lk === "langgraph_resuming" ||
      lk.endsWith("_resume") ||
      lk === "checkpoint_ns" && typeof meta[k] === "string" && String(meta[k]).includes("resume")
    ) {
      // checkpoint_ns alone is too common — only treat dedicated resume keys.
      if (lk === "checkpoint_ns") continue;
      return meta[k] != null && meta[k] !== false;
    }
  }
  return false;
}

function nameLooksResumed(name: string | null | undefined): boolean {
  if (!name) return false;
  return /(?:^|[#_\-./])resume$/i.test(name) || /-resume$/i.test(name) || /#\d+-resume$/i.test(name);
}

function outputsHaveInterrupt(outputs: unknown): boolean {
  const o = asRecord(outputs);
  if (!o) return false;
  if (o.__interrupt__ != null || o.interrupt != null) return true;
  // List-shaped interrupt payloads
  for (const k of Object.keys(o)) {
    if (k.toLowerCase().includes("interrupt") && o[k] != null) return true;
  }
  return false;
}

function errorLooksInterrupt(error: unknown): boolean {
  const t = errorText(error);
  if (!t) return false;
  return /GraphInterrupt\b/.test(t) || /\bInterrupt\s*\(/.test(t);
}

export function detectLangGraphResume(src: LangGraphFlagSource): boolean {
  if (hasCommandResumeInput(src.inputs)) return true;
  if (metadataHasResume(src.extra)) return true;
  if (nameLooksResumed(src.name)) return true;
  return false;
}

export function detectLangGraphInterrupt(src: LangGraphFlagSource): boolean {
  if (errorLooksInterrupt(src.error)) return true;
  if (outputsHaveInterrupt(src.outputs)) return true;
  // SpanSummary rows lack error bodies — gate/interrupt node name + has_error.
  if (
    src.has_error &&
    typeof src.name === "string" &&
    /(?:^|[_-])(?:human_gate|interrupt)(?:$|[_-])/i.test(src.name)
  ) {
    return true;
  }
  return false;
}

export function detectLangGraphFlags(src: LangGraphFlagSource): LangGraphFlags {
  return {
    resumed: detectLangGraphResume(src),
    interrupted: detectLangGraphInterrupt(src),
  };
}

/**
 * List-row helper when only TraceSummary fields are available (no inputs).
 * Name-based resume; interrupt inferred from error status + name heuristics
 * is intentionally NOT applied here — prefer run-level detection in detail.
 */
export function detectLangGraphFlagsFromListItem(item: {
  name?: string | null;
  status?: string | null;
  error_count?: number;
}): LangGraphFlags {
  return {
    resumed: nameLooksResumed(item.name),
    // Trace-level "interrupted" only when name suggests a gate AND errors present —
    // otherwise wait for run detail. Keep false for list to avoid noise.
    interrupted: false,
  };
}
