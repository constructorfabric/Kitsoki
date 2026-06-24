/**
 * Agent-action transcript: the raw sidecar shape + a normalized model the
 * "Agent actions" drawer renders uniformly across backends.
 *
 * Source of truth is the VERBATIM backend-native event stream (claude
 * stream-json, openai-chat, or a plugin dialect) the host teed at the wire and
 * the server returns via runstatus.session.transcript (or the static export
 * inlines into attrs.transcript). `normalizeTranscript` maps those raw events —
 * plus kitsoki's synthetic `_kitsoki` host rows — into one typed
 * `NormalizedEvent[]` the drawer renders without per-dialect branching.
 *
 * The normalized shape is intentionally OTel-GenAI-PROJECTED but is not OTel
 * itself: it is a lossy presentation view, never the stored record. The mapping
 * a future OTLP exporter would follow:
 *   kind "tool"/"mcp"  → an `execute_tool` span (gen_ai.tool.name = title)
 *   kind "reasoning"   → an assistant `chat` span content part
 *   kind "result"      → gen_ai.usage.input_tokens / output_tokens on the chat span
 *   kind "guardrail"   → a Guardrail observation (Langfuse) / a validation span
 * Keep that mapping in this comment only; the verbatim `raw` stays canonical.
 *
 * See docs/tracing/run-status-ui.md (Agent actions drawer) for the rendered
 * surface and docs/tracing/trace-format.md (Agent-action transcript sidecar)
 * for the captured event shapes (incl. the decide _kitsoki reject/nudge/accept rows).
 */

/** The runstatus.session.transcript wire shape — mirrors Go runstatus.TranscriptData. */
export interface TranscriptData {
  format: string;
  events: TranscriptEvent[];
  timings: number[];
  schemaVersion: number;
}

/** One verbatim sidecar event. Backend-native, untyped at this layer. */
export type TranscriptEvent = Record<string, unknown>;

/** The typed row the drawer renders. */
export type NormalizedKind =
  | "system" // claude init / session boot
  | "reasoning" // assistant thinking block
  | "tool" // generic tool_use + its tool_result
  | "mcp" // mcp__<server>__<tool> call
  | "guardrail" // mcp__validator__submit + _kitsoki validator_accept/reject
  | "host-nudge" // _kitsoki nudge (host-injected coaching, not a model turn)
  | "banner" // _kitsoki tool_bypassed / other host boundary marker
  | "result"; // terminal result envelope (tokens/cost)

/**
 * One normalized agent-action row. `raw` is the verbatim source event(s) so the
 * drawer can always fall back to showing the original; everything else is a
 * presentation projection. offsetMs is the capture-time ms-offset from the
 * .timings sidecar (0 when unstamped → the waterfall degrades to order).
 */
export interface NormalizedEvent {
  kind: NormalizedKind;
  title: string;
  /** Tool input args / submitted verdict (collapsible in the drawer). */
  input?: unknown;
  /** Tool result / thinking text / result text (collapsible). */
  output?: string;
  /** True for a failed step: tool_result.is_error, error result subtype, validator reject. */
  isError?: boolean;
  /** Running token usage from a result / assistant turn. */
  tokens?: { input?: number; output?: number };
  /** Accrued cost (USD) from a result envelope. */
  cost?: number;
  /** Capture-time offset (ms since call start) for the waterfall. */
  offsetMs: number;
  /** The verbatim source event(s) this row was projected from. */
  raw: TranscriptEvent | TranscriptEvent[];
}

// ── helpers ────────────────────────────────────────────────────────────────

function asRecord(v: unknown): Record<string, unknown> | undefined {
  return v && typeof v === "object" && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : undefined;
}

/** content blocks of a claude assistant/user message, tolerant of shape. */
function contentBlocks(ev: TranscriptEvent): Record<string, unknown>[] {
  const msg = asRecord(ev.message);
  const content = msg?.content;
  if (Array.isArray(content)) {
    return content.filter((c): c is Record<string, unknown> => !!asRecord(c));
  }
  return [];
}

/** Render a tool_result content payload (string or array of {text}/strings). */
function resultText(content: unknown): string {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content
      .map((c) => {
        if (typeof c === "string") return c;
        const r = asRecord(c);
        return typeof r?.text === "string" ? r.text : JSON.stringify(c);
      })
      .join("\n");
  }
  if (content == null) return "";
  return JSON.stringify(content);
}

/** mcp__<server>__<tool> → true. */
function isMcpName(name: string): boolean {
  return name.startsWith("mcp__");
}

/** The decide verdict submission tool. */
const VALIDATOR_SUBMIT = "mcp__validator__submit";

// ── normalizer ───────────────────────────────────────────────────────────────

/**
 * normalizeTranscript projects a raw transcript into the typed row stream the
 * drawer renders. It handles three dialects in one pass:
 *
 *  - claude stream-json: system/assistant(text|thinking|tool_use)/user(tool_result)/result
 *  - openai-chat (local_llm): request / assistant / tool_use / result
 *  - kitsoki synthetic `_kitsoki` rows: validator_reject / nudge / validator_accept /
 *    tool_bypassed — host-injected boundary markers interleaved in a decide arc.
 *
 * tool_use rows are paired with their tool_result (matched by id / tool_use_id)
 * so input and output land on ONE row; an unpaired tool_use still renders. A
 * tool named mcp__validator__submit is typed as a Guardrail (the verdict +
 * confidence as the input), and the following _kitsoki validator_accept/reject
 * sets its pass/fail. timings[i] stamps the row produced from events[i].
 */
export function normalizeTranscript(raw: TranscriptData): NormalizedEvent[] {
  const events = raw.events ?? [];
  const timings = raw.timings ?? [];
  const at = (i: number): number => (typeof timings[i] === "number" ? timings[i]! : 0);

  // Pre-index tool_result by tool_use_id so a tool_use row can absorb its output.
  const resultByToolID = new Map<string, { text: string; isError: boolean }>();
  events.forEach((ev) => {
    if (ev.type !== "user") return;
    for (const block of contentBlocks(ev)) {
      if (block.type === "tool_result" && typeof block.tool_use_id === "string") {
        resultByToolID.set(block.tool_use_id, {
          text: resultText(block.content),
          isError: block.is_error === true,
        });
      }
    }
  });

  const out: NormalizedEvent[] = [];
  // Track the last guardrail row so a trailing _kitsoki accept/reject can stamp it.
  let lastGuardrail: NormalizedEvent | undefined;

  // `thinking_tokens` system events are streaming token COUNTERS (estimated_tokens
  // ticking up while the model thinks), not content. Emitting a row per delta is
  // dozens of empty "SYS" lines; emitting one row at the run's START orphans a
  // counter ahead of the real thinking and lets tool calls pile up below it.
  // Instead we BUFFER a run and fold it into the `assistant` thinking block that
  // follows — that block is the authoritative thinking text and always sits in
  // order, right before the tool calls it produced. A run that is NOT followed by
  // a thinking block (a non-thinking event interrupts it, or the stream ends
  // mid-think while live) flushes one coalesced "Thinking" row at that point, so
  // the only standalone thinking row is a trailing live progress indicator.
  let pendingTokens: number | null = null;
  let pendingOffset = 0;
  let pendingRaw: TranscriptEvent[] = [];
  const flushThinking = () => {
    if (pendingTokens === null) return;
    out.push({
      kind: "reasoning",
      title: "Thinking",
      output: `≈${pendingTokens} thinking tokens`,
      offsetMs: pendingOffset,
      raw: pendingRaw,
    });
    pendingTokens = null;
    pendingRaw = [];
  };

  events.forEach((ev, i) => {
    const offsetMs = at(i);

    // Buffer a thinking-token counter delta; never a row of its own.
    if (ev.type === "system" && ev.subtype === "thinking_tokens") {
      const t =
        typeof ev.estimated_tokens === "number" ? ev.estimated_tokens : null;
      if (pendingTokens === null) pendingOffset = offsetMs;
      if (t !== null) pendingTokens = Math.max(pendingTokens ?? 0, t);
      else pendingTokens = pendingTokens ?? 0;
      pendingRaw.push(ev);
      return;
    }
    // A real thinking block right after the run consumes the counter (it renders
    // the actual text in the correct slot); anything else flushes it first.
    const consumedByBlock =
      ev.type === "assistant" &&
      contentBlocks(ev).some((b) => b.type === "thinking");
    if (!consumedByBlock) flushThinking();

    // ── kitsoki synthetic host rows ──────────────────────────────────────────
    const kit = ev["_kitsoki"];
    if (typeof kit === "string") {
      switch (kit) {
        case "validator_reject": {
          const reason = typeof ev.reason === "string" ? ev.reason : undefined;
          const src = String(ev.source ?? "schema");
          // Stamp the preceding submit verdict as REJECTED (symmetric to accept):
          // the bad verdict that triggered this reject must read as rejected, not
          // a green PASS. Without this the submit row stays unresolved and renders
          // PASS, misrepresenting the guardrail arc. Only stamp an unresolved
          // submit row (isError still undefined); else emit a standalone row.
          if (lastGuardrail && lastGuardrail.isError === undefined) {
            lastGuardrail.title = `Guardrail rejected (${src})`;
            lastGuardrail.isError = true;
            if (reason !== undefined) lastGuardrail.output = reason;
            lastGuardrail.raw = [lastGuardrail.raw as TranscriptEvent, ev].flat();
            lastGuardrail = undefined; // resolved — a later accept must not re-stamp it
            return;
          }
          out.push({
            kind: "guardrail",
            title: `Guardrail rejected (${src})`,
            output: reason,
            isError: true,
            offsetMs,
            raw: ev,
          });
          lastGuardrail = undefined;
          return;
        }
        case "validator_accept": {
          // Prefer to stamp the preceding unresolved submit row; else standalone.
          if (lastGuardrail && lastGuardrail.isError === undefined) {
            lastGuardrail.title = "Guardrail accepted";
            lastGuardrail.isError = false;
            lastGuardrail.raw = [lastGuardrail.raw as TranscriptEvent, ev].flat();
            lastGuardrail = undefined; // resolved
            return;
          }
          out.push({
            kind: "guardrail",
            title: "Guardrail accepted",
            isError: false,
            offsetMs,
            raw: ev,
          });
          return;
        }
        case "nudge":
          out.push({
            kind: "host-nudge",
            title:
              typeof ev.outer_iter === "number"
                ? `Host nudge (iteration ${ev.outer_iter})`
                : "Host nudge",
            output: typeof ev.text === "string" ? ev.text : undefined,
            offsetMs,
            raw: ev,
          });
          return;
        case "tool_bypassed":
          out.push({
            kind: "banner",
            title: "Tool bypassed",
            output:
              typeof ev.verdict_recovered_from === "string"
                ? `verdict recovered from ${ev.verdict_recovered_from}`
                : undefined,
            offsetMs,
            raw: ev,
          });
          return;
        default:
          out.push({ kind: "banner", title: `host: ${kit}`, offsetMs, raw: ev });
          return;
      }
    }

    // ── claude stream-json ──────────────────────────────────────────────────
    switch (ev.type) {
      case "system":
        out.push({
          kind: "system",
          title: `System: ${String(ev.subtype ?? "init")}`,
          output: typeof ev.model === "string" ? `model ${ev.model}` : undefined,
          offsetMs,
          raw: ev,
        });
        return;

      case "assistant": {
        // openai-chat: message.content is a plain string, not block array.
        const amsg = asRecord(ev.message);
        if (typeof amsg?.content === "string") {
          out.push({
            kind: "reasoning",
            title: "Assistant",
            output: amsg.content,
            offsetMs,
            raw: ev,
          });
          return;
        }
        for (const block of contentBlocks(ev)) {
          if (block.type === "thinking") {
            // The authoritative thinking text — folds in any buffered token run
            // (the leading counters that streamed while this block was forming).
            const tokenNote =
              pendingTokens !== null && pendingTokens > 0
                ? ` (≈${pendingTokens} tokens)`
                : "";
            pendingTokens = null;
            pendingRaw = [];
            out.push({
              kind: "reasoning",
              title: `Reasoning${tokenNote}`,
              output: typeof block.thinking === "string" ? block.thinking : "",
              offsetMs,
              raw: ev,
            });
          } else if (block.type === "text") {
            out.push({
              kind: "reasoning",
              title: "Assistant",
              output: typeof block.text === "string" ? block.text : "",
              offsetMs,
              raw: ev,
            });
          } else if (block.type === "tool_use") {
            const name = typeof block.name === "string" ? block.name : "tool";
            const id = typeof block.id === "string" ? block.id : "";
            const res = id ? resultByToolID.get(id) : undefined;
            if (name === VALIDATOR_SUBMIT) {
              const row: NormalizedEvent = {
                kind: "guardrail",
                title: "Guardrail: submit verdict",
                input: block.input,
                output: res?.text,
                isError: res?.isError === true ? true : undefined,
                offsetMs,
                raw: ev,
              };
              out.push(row);
              lastGuardrail = row;
            } else {
              out.push({
                kind: isMcpName(name) ? "mcp" : "tool",
                title: name,
                input: block.input,
                output: res?.text,
                isError: res?.isError === true ? true : undefined,
                offsetMs,
                raw: ev,
              });
            }
          }
        }
        return;
      }

      case "user":
        // tool_result already absorbed into its tool_use row above. Skip.
        return;

      case "result": {
        const usage = asRecord(ev.usage);
        const tokens =
          usage &&
          (typeof usage.input_tokens === "number" ||
            typeof usage.output_tokens === "number")
            ? {
                input:
                  typeof usage.input_tokens === "number"
                    ? usage.input_tokens
                    : undefined,
                output:
                  typeof usage.output_tokens === "number"
                    ? usage.output_tokens
                    : undefined,
              }
            : undefined;
        out.push({
          kind: "result",
          title: `Result: ${String(ev.subtype ?? "success")}`,
          output: typeof ev.result === "string" ? ev.result : undefined,
          isError: ev.subtype != null && ev.subtype !== "success" ? true : undefined,
          tokens,
          cost:
            typeof ev.total_cost_usd === "number" ? ev.total_cost_usd : undefined,
          offsetMs,
          raw: ev,
        });
        return;
      }

      // ── openai-chat (local_llm) ───────────────────────────────────────────
      case "request":
        out.push({
          kind: "system",
          title: "Request",
          output: typeof asRecord(ev.message)?.content === "string"
            ? (asRecord(ev.message)!.content as string)
            : undefined,
          offsetMs,
          raw: ev,
        });
        return;

      case "tool_use": {
        // openai-chat flat tool_use (also tolerated): {id,name,arguments}
        const name = typeof ev.name === "string" ? ev.name : "tool";
        out.push({
          kind: isMcpName(name) ? "mcp" : "tool",
          title: name,
          input: ev.arguments ?? ev.input,
          offsetMs,
          raw: ev,
        });
        return;
      }

      default:
        // Unknown event: surface as a banner so nothing is silently dropped.
        out.push({
          kind: "banner",
          title: typeof ev.type === "string" ? String(ev.type) : "event",
          offsetMs,
          raw: ev,
        });
        return;
    }
  });

  // A token run still pending at the end never met its thinking block — the stream
  // ends mid-think (a live call still reasoning). Surface it as a single trailing
  // "Thinking…" progress row at the bottom, where the newest activity belongs.
  flushThinking();

  return out;
}

// ── cassette-vs-live drift diff ──────────────────────────────────────────────

/** One row of a tool-call-path diff between a recorded and a live transcript. */
export interface DiffRow {
  /** "same" — both sides ran this tool; "added" — only live; "removed" — only recorded. */
  status: "same" | "added" | "removed";
  /** The tool-call title (the row's normalized title) on whichever side is present. */
  title: string;
  /** The recorded-side title at this position (undefined for an "added" row). */
  recorded?: string;
  /** The live-side title at this position (undefined for a "removed" row). */
  live?: string;
}

export interface TranscriptDiff {
  /** True when the two tool-call paths are identical in order and name. */
  identical: boolean;
  rows: DiffRow[];
}

/** The kinds whose ordered sequence defines the "tool-call path" we diff. */
const PATH_KINDS = new Set<NormalizedKind>(["tool", "mcp", "guardrail"]);

/** The ordered tool-call titles that make up a transcript's tool path. */
function toolPath(rows: NormalizedEvent[]): string[] {
  return rows.filter((r) => PATH_KINDS.has(r.kind)).map((r) => r.title);
}

/**
 * diffToolPaths compares the recorded transcript's tool-call path against a
 * fresh live one and flags drift. It is a pure consumer of two transcripts (the
 * determinism-frontier capability: replay alone never produces a live
 * transcript to diff — see the proposal's §Determinism). A classic LCS-free
 * positional walk is enough here: the path is short and ordered, and an operator
 * reads "the live run took a different tool at step N" most clearly as a
 * positional added/removed pair rather than a minimal edit script.
 */
export function diffToolPaths(
  recorded: TranscriptData,
  live: TranscriptData
): TranscriptDiff {
  const a = toolPath(normalizeTranscript(recorded));
  const b = toolPath(normalizeTranscript(live));
  const rows: DiffRow[] = [];
  let identical = a.length === b.length;
  const n = Math.max(a.length, b.length);
  for (let i = 0; i < n; i++) {
    const ra = a[i];
    const rb = b[i];
    if (ra !== undefined && rb !== undefined) {
      if (ra === rb) {
        rows.push({ status: "same", title: ra, recorded: ra, live: rb });
      } else {
        identical = false;
        rows.push({ status: "removed", title: ra, recorded: ra });
        rows.push({ status: "added", title: rb, live: rb });
      }
    } else if (ra !== undefined) {
      identical = false;
      rows.push({ status: "removed", title: ra, recorded: ra });
    } else if (rb !== undefined) {
      identical = false;
      rows.push({ status: "added", title: rb, live: rb });
    }
  }
  return { identical, rows };
}
