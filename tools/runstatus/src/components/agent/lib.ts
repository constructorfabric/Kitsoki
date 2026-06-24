/** Shared helpers for agent sub-renderers. */

export const TRUNCATE_LIMIT = 500;

export function prettyJson(val: unknown): string {
  return JSON.stringify(val, null, 2);
}

export function isTruncated(s: string): boolean {
  return s.length > TRUNCATE_LIMIT;
}

export function maybeShow(val: string, expanded: boolean): string {
  if (!expanded && val.length > TRUNCATE_LIMIT) {
    return val.slice(0, TRUNCATE_LIMIT) + "…";
  }
  return val;
}

export function fmtMs(ms: unknown): string {
  if (typeof ms !== "number") return "—";
  if (ms >= 1000) return (ms / 1000).toFixed(2) + "s";
  return ms + "ms";
}

export function fmtTokens(n: unknown): string {
  if (typeof n !== "number") return "—";
  return n.toLocaleString();
}

export function fmtCost(usd: unknown): string {
  if (typeof usd !== "number") return "";
  return "$" + usd.toFixed(4);
}

/** Token usage + cost for a single agent.call.complete event. */
export interface AgentUsage {
  promptTokens?: number;
  responseTokens?: number;
  cacheReadTokens?: number;
  cacheCreationTokens?: number;
  costUsd?: number;
}

function asNum(v: unknown): number | undefined {
  return typeof v === "number" ? v : undefined;
}

/**
 * Read token usage + cost from an agent.call.complete event's attrs.
 *
 * The canonical shape the engine emits is the opaque transport `meta`:
 *   attrs.meta = { usage: { input_tokens, output_tokens,
 *                           cache_read_input_tokens, cache_creation_input_tokens },
 *                  cost_usd }
 * (the raw claude-CLI usage object — see docs AGENT_ATTRS.md). We fall back to
 * two legacy aliases so older/synthetic fixtures still render: flat top-level
 * `prompt_tokens`/`response_tokens`/`cost_usd`, and the earlier cassette form
 * `meta.prompt_tokens`/`meta.response_tokens`.
 */
export function readAgentUsage(attrs: Record<string, unknown> | undefined): AgentUsage {
  const a = attrs ?? {};
  const meta = (a.meta ?? {}) as Record<string, unknown>;
  const usage = (meta.usage ?? {}) as Record<string, unknown>;
  return {
    promptTokens: asNum(usage.input_tokens) ?? asNum(meta.prompt_tokens) ?? asNum(a.prompt_tokens),
    responseTokens: asNum(usage.output_tokens) ?? asNum(meta.response_tokens) ?? asNum(a.response_tokens),
    cacheReadTokens: asNum(usage.cache_read_input_tokens),
    cacheCreationTokens: asNum(usage.cache_creation_input_tokens),
    costUsd: asNum(meta.cost_usd) ?? asNum(a.cost_usd),
  };
}

/** Tool name → colour category for the transcript chip. */
export function toolChipClass(tool: string): string {
  const t = tool.toLowerCase();
  if (t === "edit" || t === "write" || t === "multiedit") return "tool-chip--edit";
  if (t === "bash" || t === "shell" || t === "run") return "tool-chip--bash";
  if (t === "read" || t === "grep" || t === "glob" || t === "find") return "tool-chip--read";
  return "tool-chip--default";
}
