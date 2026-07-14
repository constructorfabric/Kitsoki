/**
 * audit.ts — swarm-side aggregation over tools/runstatus's ui-audit.ts
 * findings.
 *
 * The actual browser-side probing (page.evaluate(geometryProbe), the
 * AxeBuilder pass) stays in the spec file — that keeps tools/swarm free of
 * the @axe-core/playwright and @playwright/test runtime dependencies (it
 * only takes a TYPE-ONLY import of RawFinding/Severity, which is erased at
 * build time and needs no npm package resolution from tools/swarm/). This
 * module owns the POLICY: which findings gate a user's journey, and the
 * per-state dedupe so a probe (especially the expensive axe pass) runs once
 * per distinct page state rather than once per user per state.
 */
import type { RawFinding, Severity } from "../runstatus/tests/playwright/lib/ui-audit.js";

export type { RawFinding, Severity };

/** A finding tagged with which probe produced it — see `gatingErrors`'s doc
 *  comment for why this distinction matters for what the swarm gates on. */
export interface TaggedFinding extends RawFinding {
  source: "geometry" | "a11y";
}

/** One state's audit outcome, tagged for the results JSON. */
export interface AuditRecord {
  state: string;
  findings: TaggedFinding[];
  error_count: number;
}

/**
 * Only GEOMETRY "error"-severity findings gate the swarm (page-horizontal-
 * scroll, offscreen-clip, content-clipped, stray-template-token — the
 * classes of defect a concurrent/isolation bug could actually cause: broken
 * layout, leaked template state). axe a11y "error" findings are real, but
 * this repo's own precedent (tools/runstatus/tests/playwright/tour-review.spec.ts,
 * which runs the exact same geometryProbe + AxeBuilder pair) NEVER hard-gates
 * a spec on zero a11y findings — it records them for the separate
 * kitsoki-ui-review human/LLM pass. A live 24-way run of this harness found
 * real, PRE-EXISTING a11y debt on the chat surface (color-contrast,
 * unlabeled selects, a non-focusable scrollable region) that has nothing to
 * do with swarm concurrency and would fail ANY spec exercising this surface,
 * concurrent or not. Gating this tier's standing CI check on fixing
 * site-wide a11y debt (out of tools/swarm/**'s scope) would make the gate
 * permanently, unactionably red — so a11y findings are collected and
 * reported (see the results JSON's `audit_a11y_advisory` field) but do not
 * fail the run, matching the existing tour-review precedent.
 */
export function gatingErrors(findings: TaggedFinding[]): TaggedFinding[] {
  return findings.filter((f) => f.source === "geometry" && f.severity === ("error" as Severity));
}

/** a11y "error" findings — informational for this tier (see `gatingErrors`). */
export function advisoryA11yErrors(findings: TaggedFinding[]): TaggedFinding[] {
  return findings.filter((f) => f.source === "a11y" && f.severity === ("error" as Severity));
}

export function recordFor(state: string, findings: TaggedFinding[]): AuditRecord {
  return { state, findings, error_count: gatingErrors(findings).length };
}

/**
 * Tracks which (state) buckets have already had the expensive axe pass run,
 * shared across ALL concurrent users in one swarm run — the rendered DOM
 * structure for a given FSM state is the same story/room regardless of which
 * user's session is showing it (only the transcript's marker text differs,
 * which axe's structural rules don't care about). Mirrors the existing
 * tour-review.spec.ts precedent (axe cached per route+viewport, not re-run
 * per step) at swarm scale (per state, not per user).
 */
export class SharedAxeGate {
  private readonly done = new Set<string>();

  /** Returns true the FIRST time `state` is seen (caller should run axe),
   *  false on every subsequent call (caller should skip it). */
  claim(state: string): boolean {
    if (this.done.has(state)) return false;
    this.done.add(state);
    return true;
  }
}
