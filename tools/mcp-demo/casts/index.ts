/**
 * The cast registry — the agent-agnostic seam. Claude Code is the POC; codex and
 * copilot slot in as additional entries (each a synthetic cast now, swapped for a
 * gated live capture later). The spec selects by `MCP_DEMO_AGENT` (default
 * claude-code), or loads a captured JSON cast verbatim via `MCP_DEMO_CAST_JSON`.
 */
import fs from "fs";
import { type Termcast } from "./types.js";
import { cast as claudeCode } from "./claude-code.cast.js";

export const CASTS: Record<string, Termcast> = {
  "claude-code": claudeCode,
  // "codex": codex,      // ← add when the codex cast is authored/captured
  // "copilot": copilot,  // ← add when the copilot cast is authored/captured
};

/** Resolve the cast for this recording pass: captured JSON override wins. */
export function resolveCast(): Termcast {
  const jsonPath = process.env.MCP_DEMO_CAST_JSON;
  if (jsonPath) {
    const raw = JSON.parse(fs.readFileSync(jsonPath, "utf8")) as Termcast;
    if (!raw.beats?.length) throw new Error(`captured cast has no beats: ${jsonPath}`);
    return raw;
  }
  const agent = process.env.MCP_DEMO_AGENT ?? "claude-code";
  const c = CASTS[agent];
  if (!c) throw new Error(`unknown MCP_DEMO_AGENT "${agent}" (have: ${Object.keys(CASTS).join(", ")})`);
  return c;
}
