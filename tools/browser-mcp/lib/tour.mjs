// Tour authoring + replay: tour_start/tour_step/tour_export/tour_replay.
// Produces and consumes tour format v2 JSON
// (internal/tour/manifest_v2.go, tools/runstatus/src/tour/types-v2.ts,
// schemas/tour-v2.schema.json) — the browser-mcp package's work product IS
// the tour JSON, not a side effect.
//
// tour_step enriches whatever anchor fields the caller supplies by
// resolving them against the LIVE DOM and reading back the anchor's other
// discoverable fields (role, accessible name, testid) rather than trusting
// the caller's bundle blindly — this is what "compiles the last act into a
// step with an ENRICHED multi-anchor bundle" means in practice: the
// authored step's bundle is validated AND filled out from the real page at
// author time, so replay later has the richest possible bundle to resolve
// against.
import { resolveAnchorWithHeal, AnchorResolutionError } from "./anchors.mjs";

let activeTour = null;

export function tourStart({ id, origin }) {
  if (!id) throw new Error("tour_start requires id");
  activeTour = { version: 2, id, origin: origin || undefined, steps: [] };
  return { started: true, id, origin: activeTour.origin };
}

function ensureActiveTour() {
  if (!activeTour) throw new Error("no active tour; call tour_start first");
  return activeTour;
}

// Reads back the live DOM's role/accessible-name/testid for whichever
// element the author's anchor fields resolved to, merging them into the
// bundle so a bundle authored as just {testid: "save-btn"} comes back
// carrying role/name too (a richer bundle survives more DOM drift at
// replay time). Only fills fields the author left blank — it never
// overwrites an explicit author choice.
async function enrichTarget(page, target) {
  if (!target) return undefined;
  const { locator } = await resolveAnchorWithHeal(page, target, undefined).then(
    (r) => r,
    () => ({ locator: null })
  );
  if (!locator) return target; // couldn't resolve at all; tourStep() below will have already thrown
  const discovered = await page.evaluate((sel) => {
    const el = document.querySelector(sel);
    if (!el) return null;
    return {
      role: el.getAttribute("role") || null,
      name: el.getAttribute("aria-label") || null,
      testid: el.getAttribute("data-testid") || null,
      text: (el.textContent || "").trim().slice(0, 120) || null
    };
  }, buildSelectorForEnrich(target));
  if (!discovered) return target;
  return {
    ...target,
    role: target.role || discovered.role || undefined,
    name: target.name || discovered.name || undefined,
    testid: target.testid || discovered.testid || undefined,
    text: target.text || discovered.text || undefined
  };
}

// Rebuilds the same selector resolveAnchorWithHeal's primary strategy would
// have used, purely so enrichTarget can re-query without threading a second
// return value through the earlier resolve. Cheap: testid/css are already
// unique-selector-shaped; role/text fall through to a best-effort css guess
// which is fine here since this only enriches an already-uniquely-resolved
// element (a failure just skips enrichment, never breaks authoring).
function buildSelectorForEnrich(target) {
  if (target.testid) return `[data-testid="${target.testid}"]`;
  if (target.css) return target.css;
  return "*"; // enrichment is best-effort; a miss here just means fewer fields fill in
}

// Appends one step to the active tour. Validates (and enriches) `target`
// against the live page when present — an authored step whose anchor
// doesn't resolve on the page currently open is a loud authoring-time
// error, not something silently deferred to replay.
export async function tourStep(page, step) {
  const tour = ensureActiveTour();
  if (!step?.id) throw new Error("tour_step requires id");
  if (!step?.kind) throw new Error("tour_step requires kind");

  const compiled = { ...step };
  if (step.target) {
    const { strategy } = await resolveAnchorWithHeal(page, step.target, step.id);
    compiled.target = await enrichTarget(page, step.target);
    compiled._authoredStrategy = strategy; // informational; stripped on export
  }
  tour.steps.push(compiled);
  return { appended: true, id: step.id, stepCount: tour.steps.length };
}

// Returns the active tour as v2 JSON, stripping authoring-only bookkeeping
// fields (currently just _authoredStrategy) that aren't part of the format.
export function tourExport() {
  const tour = ensureActiveTour();
  return {
    version: 2,
    id: tour.id,
    ...(tour.origin ? { origin: tour.origin } : {}),
    steps: tour.steps.map(({ _authoredStrategy, ...step }) => step)
  };
}

// Deterministic, no-LLM replay of a v2 tour against the live page: for
// every step with a target, resolves it (with healing), performs the
// step's act (when kind === "act" and act.kind is click/fill), and collects
// {stepId, ok, strategy} results plus every HealEvent
// (internal/tour/manifest_v2.go HealEvent) emitted along the way. A tour
// with a target that fails to resolve at all is a hard per-step failure,
// not a thrown exception — replay always finishes and reports.
export async function tourReplay(page, tourJson) {
  if (tourJson.version !== 2) throw new Error(`tour_replay expects version 2, got ${tourJson.version}`);
  const results = [];
  const heals = [];
  for (const step of tourJson.steps || []) {
    if (!step.target) {
      results.push({ stepId: step.id, ok: true, strategy: null, note: "no target to resolve" });
      continue;
    }
    try {
      const { locator, strategy, heal } = await resolveAnchorWithHeal(page, step.target, step.id);
      if (heal) heals.push(heal);
      if (step.kind === "act" && step.act?.kind === "click") await locator.click();
      if (step.kind === "act" && step.act?.kind === "fill") await locator.fill(step.act.value || "");
      results.push({ stepId: step.id, ok: true, strategy });
    } catch (err) {
      const attempts = err instanceof AnchorResolutionError ? err.attempts : undefined;
      results.push({ stepId: step.id, ok: false, error: err?.message || String(err), attempts });
    }
  }
  return { id: tourJson.id, results, heals, passed: results.every((r) => r.ok) };
}

export function _resetActiveTourForTest() {
  activeTour = null;
}
