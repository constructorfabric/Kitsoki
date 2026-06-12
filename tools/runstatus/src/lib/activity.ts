/**
 * activity.ts — the shared model of an agent's live activity feed.
 *
 * Both chat surfaces (the main chat's turn-stream and the meta overlay's
 * meta-stream) render the same thing while a turn is in flight: the agent's
 * 🧠 thoughts interleaved with its tool calls, in ARRIVAL order, preserved
 * collapsed inside the finished message. This module owns the data shape and
 * the accumulation rules so the two stores cannot drift; the matching
 * presentation lives in components/ActivityFeed.vue + ActivityDisclosure.vue.
 */

/**
 * One item of a live stream feed, in ARRIVAL ORDER. The server emits one
 * "think"/"delta" frame per assistant thought and one "tool" frame per tool
 * call, already interleaved the way the model produced them; keeping a single
 * ordered list preserves that — splitting thoughts and tools into separate
 * buckets (the old shape) re-ordered the feed so every tool call rendered
 * above the thinking it followed.
 */
export type StreamItem =
  | { kind: "thinking"; text: string }
  | { kind: "tool"; tool: string; preview: string };

/**
 * Append a thought to the feed, merging with a trailing thinking item.
 *
 * Delta granularity varies by sender: claude emits one COMPLETE thought per
 * frame, while chunked senders (the no-LLM stub) emit word fragments with
 * trailing spaces. Consecutive thoughts merge into one thinking item either
 * way — a fragment (prior text ends in whitespace) continues inline, a
 * complete thought starts a new paragraph. A tool item ends the run, so the
 * next thought gets its own item below that tool.
 *
 * Mutates `feed` in place when merging; pushes a fresh item otherwise.
 */
export function appendThought(feed: StreamItem[], text: string): void {
  if (!text) return;
  const last = feed[feed.length - 1];
  if (last?.kind === "thinking") {
    last.text += (/\s$/.test(last.text) ? "" : "\n\n") + text;
  } else {
    feed.push({ kind: "thinking", text });
  }
}

/** Append a tool-call breadcrumb to the feed. */
export function appendTool(feed: StreamItem[], tool: string, preview: string): void {
  if (!tool) return;
  feed.push({ kind: "tool", tool, preview });
}

/** Summary line for the collapsed activity feed: "🧠 2 thoughts · 3 tool calls". */
export function activityLabel(stream: StreamItem[]): string {
  const thoughts = stream.filter((it) => it.kind === "thinking").length;
  const tools = stream.length - thoughts;
  const parts: string[] = [];
  if (thoughts > 0) parts.push(`${thoughts} ${thoughts === 1 ? "thought" : "thoughts"}`);
  if (tools > 0) parts.push(`${tools} ${tools === 1 ? "tool call" : "tool calls"}`);
  return `🧠 ${parts.join(" · ")}`;
}
