/**
 * `termcast` — the agent-agnostic cassette the MCP terminal demo replays.
 *
 * A termcast is a list of narrated BEATS; each beat is a chapter in the recorded
 * video (its id/label become the `<video>.chapters.json` rail) and carries the
 * terminal CHUNKS to play plus the caption to show while they play. Two chunk
 * kinds: `type` (operator typing, replayed char-by-char) and `out` (agent / tool
 * output, written fast). `data` may contain ANSI SGR — xterm renders it.
 *
 * The SAME shape is produced two ways (epic decision: "both synthetic and
 * recorded"): hand-authored synthetic casts (this dir, no LLM — the deterministic
 * proof) and casts captured from one gated live `claude` ↔ `kitsoki mcp` session
 * then segmented into beats. The replay can't tell them apart, and neither path
 * calls a model at render time.
 */

export interface TermChunk {
  /** `type` = operator keystrokes (char-by-char); `out` = agent/tool output. */
  kind: "type" | "out";
  data: string;
}

export interface TermBeat {
  /** Stable chapter id (→ chapters.json rail; keep unique within a cast). */
  id: string;
  /** Chapter label for the rail / pacing report. */
  label: string;
  /** Narration banner title shown while this beat plays. */
  caption: string;
  /** Optional narration subtitle. */
  sub?: string;
  /** The terminal chunks to replay for this beat, in order. */
  chunks: TermChunk[];
  /** Watch-speed dwell (ms) after the chunks land, so the beat is readable. */
  holdMs?: number;
}

export interface Termcast {
  /** Which external agent this records — "claude-code" | "codex" | "copilot". */
  agent: string;
  /** Terminal window titlebar text. */
  title: string;
  /** Optional title curtain text. Defaults to title. */
  curtainTitle?: string;
  /** Optional footer text under the terminal stage. */
  footer?: string;
  /** Optional titlebar badge. */
  badge?: string;
  /** Text fragments the recorder asserts after replay. Defaults to ["kitsoki"]. */
  assertText?: string[];
  cols: number;
  rows: number;
  beats: TermBeat[];
}

// ── ANSI SGR helpers (authoring convenience for synthetic casts) ──────────────
const E = "\x1b[";
const wrap = (code: string) => (s: string): string => `${E}${code}m${s}${E}0m`;
export const ansi = {
  reset: `${E}0m`,
  dim: wrap("2"),
  bold: wrap("1"),
  gray: wrap("90"),
  red: wrap("31"),
  green: wrap("32"),
  yellow: wrap("33"),
  blue: wrap("34"),
  magenta: wrap("35"),
  cyan: wrap("36"),
  white: wrap("97"),
};
