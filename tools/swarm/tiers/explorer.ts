/**
 * explorer.ts — tier 3: the gated LIVE persona-explorer dispatch contract.
 *
 * Tiers 1-2 are structurally token-free (scripted intents / a recorded
 * cassette). Tier 3 is the opposite: a small number of REAL, headless,
 * live-LLM persona agents, each holding its own browser context, exploring a
 * running `kitsoki web` server autonomously and journaling what they find —
 * genuine LLM spend, every time it runs. Per
 * docs/goals/ui-qa-scale/GOAL.md's G5 ("live explorer users are few,
 * explicitly gated, and every live capability has a standing no-LLM replay
 * gate in CI"), this module is the GATING/DISPATCH contract only — pure TS,
 * no browser, no subprocess, no LLM call of its own. The actual browser +
 * agent wiring (real spend) lives in `liveExplorerCli.ts`, which imports and
 * calls THIS module's `dispatchExplorers`; the standing CI spec
 * (swarm-cassette-users.spec.ts) exercises `dispatchExplorers` with an
 * injected STUB `runExplorer` so the gating/budget/journaling CONTRACT is
 * proven green in CI without ever constructing a real explorer.
 *
 * Structural safety (why this can't run by accident):
 *   1. `assertLiveExplorersAllowed` is the ONLY gate, and it takes a single
 *      required boolean with no default value anywhere in this module — the
 *      caller must explicitly pass `liveExplorers: true`. `liveExplorerCli.ts`
 *      is the ONE place that boolean is threaded from `process.argv`
 *      (`--live-explorers`), never from an env var, a config file default,
 *      or a fallback expression — an operator has to type the flag.
 *   2. `resolveExplorerBudget` HARD-CLAMPS to `MAX_LIVE_EXPLORERS` (3)
 *      regardless of what the caller asks for — there is no code path that
 *      can request more than 3 concurrent live agents.
 *   3. `dispatchExplorers` is the only exported way to run explorers, and it
 *      calls `assertLiveExplorersAllowed` FIRST, before touching `personas`,
 *      `runExplorer`, or the budget — a caller cannot bypass the gate by
 *      calling some other lower-level entry point, because there isn't one.
 */
import type { Persona } from "../personas.js";

/** Hard ceiling on concurrent live explorers, per
 *  docs/goals/ui-qa-scale/decomposition.yaml's swarm-tiers23 acceptance
 *  ("<=3"). Not configurable upward by any caller — see `resolveExplorerBudget`. */
export const MAX_LIVE_EXPLORERS = 3;

/** Thrown by `assertLiveExplorersAllowed` / `dispatchExplorers` when the
 *  caller did not explicitly opt in. The message is deliberately explicit
 *  about WHY, so a confused CI log or accidental invocation reads as a
 *  refusal, not a crash. */
export class LiveExplorersNotAllowedError extends Error {
  constructor() {
    super(
      "tier-3 live explorers require an explicit --live-explorers flag; refusing to dispatch " +
        "(this is real LLM spend and must never run by accident — see tools/swarm/README.md).",
    );
    this.name = "LiveExplorersNotAllowedError";
  }
}

/** The ONE gate. `liveExplorers` must be `=== true`; anything else (undefined,
 *  falsy, a truthy-but-non-boolean value from a misparsed flag) refuses. */
export function assertLiveExplorersAllowed(opts: { liveExplorers: boolean }): void {
  if (opts.liveExplorers !== true) {
    throw new LiveExplorersNotAllowedError();
  }
}

/** Clamps a requested explorer count to `[0, MAX_LIVE_EXPLORERS]`. Never
 *  returns more than the hard ceiling no matter what `requested` is (a
 *  negative or NaN request clamps to 0, not by escaping the ceiling). */
export function resolveExplorerBudget(requested: number): number {
  const n = Number.isFinite(requested) ? requested : 0;
  return Math.max(0, Math.min(MAX_LIVE_EXPLORERS, Math.floor(n)));
}

/** What one dispatched explorer is briefed with. `lens` mirrors
 *  `tools/product-journey/run.py`'s `persona_lens()` shape (starting_surface,
 *  first_question, evidence_emphasis, escalation_trigger, finding_bias) —
 *  duplicated here as plain data (not a python subprocess call) because the
 *  briefing TEXT quality is not what this module's contract is about; the
 *  dispatch/budget/journaling wiring is. `liveExplorerCli.ts` populates a
 *  real lens per persona id when it constructs this. */
export interface ExplorerLens {
  starting_surface: string;
  first_question: string;
  evidence_emphasis: string;
  escalation_trigger: string;
  finding_bias: string;
}

export interface ExplorerBriefing {
  persona: Persona;
  lens: ExplorerLens;
  /** The kitsoki web server this explorer should mint its own session
   *  against (shared with tiers 1-2, per the acceptance's "small number of
   *  live persona agents" running alongside the replay tiers — see
   *  tools/swarm/README.md's manual live-acceptance procedure). */
  serverBase: string;
  /** The product-journey run bundle directory findings are journaled into
   *  via `--record-finding` / `--record-blocker` (tools/product-journey/run.py). */
  runDir: string;
}

/** One explorer's reported outcome. `findingsRecorded` / `blockersRecorded`
 *  are counts the runner reports back (it owns the actual `--record-finding`
 *  / `--record-blocker` subprocess calls) so `dispatchExplorers` can total
 *  them without knowing product-journey's CLI itself. */
export interface ExplorerOutcome {
  persona_id: string;
  ok: boolean;
  findings_recorded: number;
  blockers_recorded: number;
  /** Best-effort cost estimate in USD, if the runner's agent backend reports
   *  usage; undefined when unknown (never assume $0 — see `dispatchExplorers`'s
   *  doc comment on totalCostUsd). */
  cost_usd?: number;
  error?: string;
}

/** DI seam: the actual live browser+agent driver, injected by the caller.
 *  `liveExplorerCli.ts` supplies the REAL implementation (spawns a headless
 *  browser context + a live agent subprocess); the standing CI spec supplies
 *  a STUB that returns a canned outcome instantly with no browser and no
 *  subprocess, proving the surrounding contract (gating, budget, aggregation)
 *  without ever calling an LLM. */
export type RunExplorerFn = (briefing: ExplorerBriefing, index: number) => Promise<ExplorerOutcome>;

export interface DispatchExplorersOptions {
  /** MUST be explicitly true (see `assertLiveExplorersAllowed`). */
  liveExplorers: boolean;
  /** Requested explorer count; clamped to `[0, MAX_LIVE_EXPLORERS]` by
   *  `resolveExplorerBudget` regardless of this value. */
  requestedCount: number;
  personas: Persona[];
  serverBase: string;
  runDir: string;
  /** Mirrors `tools/product-journey/run.py`'s `persona_lens()`; see
   *  `ExplorerBriefing.lens`'s doc comment. Falls back to a generic lens
   *  derived from the persona itself when a given persona id has no entry —
   *  matching `persona_lens()`'s own default-lens fallback. */
  lensFor: (persona: Persona) => ExplorerLens;
  runExplorer: RunExplorerFn;
}

export interface DispatchExplorersResult {
  dispatched: number;
  outcomes: ExplorerOutcome[];
  all_ok: boolean;
  total_findings_recorded: number;
  total_blockers_recorded: number;
  /** Sum of every outcome's `cost_usd` that WAS reported; `null` if none of
   *  the dispatched explorers reported a cost (never fabricated as 0 — an
   *  unknown-cost run must read as unknown, not as free). */
  total_cost_usd: number | null;
}

/**
 * The tier-3 entry point. Refuses immediately (before touching personas,
 * budget, or the runner) unless `opts.liveExplorers === true`. Otherwise
 * clamps to at most `MAX_LIVE_EXPLORERS` personas (cycling
 * `opts.personas` deterministically, same index-based scheme
 * `personaForIndex` uses for tiers 1-2), dispatches them THROUGH
 * `opts.runExplorer` (never anything else), and aggregates the outcomes.
 * Explorers run concurrently (each owns its own browser context / agent
 * process — there is no shared mutable state between them at this layer),
 * so a slow or hung explorer does not block the others.
 */
export async function dispatchExplorers(opts: DispatchExplorersOptions): Promise<DispatchExplorersResult> {
  assertLiveExplorersAllowed({ liveExplorers: opts.liveExplorers });

  const n = resolveExplorerBudget(opts.requestedCount);
  if (opts.personas.length === 0) {
    throw new Error("dispatchExplorers: personas must be non-empty");
  }

  const briefings: ExplorerBriefing[] = Array.from({ length: n }, (_, i) => {
    const persona = opts.personas[i % opts.personas.length];
    return {
      persona,
      lens: opts.lensFor(persona),
      serverBase: opts.serverBase,
      runDir: opts.runDir,
    };
  });

  const outcomes = await Promise.all(briefings.map((b, i) => opts.runExplorer(b, i)));

  let totalCost: number | null = null;
  for (const o of outcomes) {
    if (typeof o.cost_usd === "number") {
      totalCost = (totalCost ?? 0) + o.cost_usd;
    }
  }

  return {
    dispatched: outcomes.length,
    outcomes,
    all_ok: outcomes.every((o) => o.ok),
    total_findings_recorded: outcomes.reduce((s, o) => s + o.findings_recorded, 0),
    total_blockers_recorded: outcomes.reduce((s, o) => s + o.blockers_recorded, 0),
    total_cost_usd: totalCost,
  };
}
