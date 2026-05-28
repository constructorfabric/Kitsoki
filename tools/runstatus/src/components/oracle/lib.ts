/** Shared helpers for oracle sub-renderers. */

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

/** Tool name → colour category for the transcript chip. */
export function toolChipClass(tool: string): string {
  const t = tool.toLowerCase();
  if (t === "edit" || t === "write" || t === "multiedit") return "tool-chip--edit";
  if (t === "bash" || t === "shell" || t === "run") return "tool-chip--bash";
  if (t === "read" || t === "grep" || t === "glob" || t === "find") return "tool-chip--read";
  return "tool-chip--default";
}
