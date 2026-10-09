/**
 * results.ts — the swarm's per-run results JSON schema + writer.
 *
 * Written under .artifacts/swarm/ (gitignored per AGENTS.md — this is a
 * generated artifact, never committed). Later tiers/changes (swarm-capture,
 * swarm-arena-job) are expected to read this shape, so it is kept flat and
 * plain-JSON (no class instances) even though it is only produced here today.
 */
import fs from "fs";
import path from "path";
import type { RssSummary } from "./rss.js";
import type { TaggedFinding } from "./audit.js";

export interface UserResult {
  index: number;
  persona_id: string;
  session_id: string;
  marker: string;
  completed: boolean;
  states_visited: string[];
  console_errors: number;
  console_error_samples: string[];
  /** GATING geometry-probe error findings (see audit.ts's gatingErrors doc
   *  comment for why axe a11y findings are reported separately, not here). */
  audit_error_count: number;
  audit_error_samples: TaggedFinding[];
  /** Informational only — pre-existing a11y debt on the surface, not a
   *  swarm-introduced regression; never gates the run (audit.ts's
   *  advisoryA11yErrors doc comment has the full rationale). */
  audit_a11y_advisory_count: number;
  audit_a11y_advisory_samples: TaggedFinding[];
  isolation_ok: boolean;
  isolation_leaked: string[];
  duration_ms: number;
  error?: string;
}

export interface NegativeControlResult {
  description: string;
  shared_session_id: string;
  injected_marker: string;
  detected: boolean;
  leaked: string[];
}

export interface SwarmResults {
  run_id: string;
  started_at: string;
  ended_at: string;
  server: { addr: string; flow: string };
  user_count: number;
  users: UserResult[];
  all_completed: boolean;
  all_isolated: boolean;
  all_console_clean: boolean;
  all_audit_clean: boolean;
  rss: RssSummary;
  negative_control: NegativeControlResult;
}

/** Writes `results` as pretty JSON under `.artifacts/swarm/results-<run_id>.json`
 *  (creating the directory if needed) and returns the path written. */
export function writeResults(artifactsDir: string, results: SwarmResults): string {
  fs.mkdirSync(artifactsDir, { recursive: true });
  const outPath = path.join(artifactsDir, `results-${results.run_id}.json`);
  fs.writeFileSync(outPath, JSON.stringify(results, null, 2) + "\n");
  return outPath;
}
