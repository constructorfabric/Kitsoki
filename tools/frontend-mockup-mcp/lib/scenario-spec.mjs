// Scenario spec (*.mockup.json, version 1) primitives shared by
// create-mockup.mjs and its tests. Implements contract §7 item 2
// (~/code/POG/.context/mockup-demo-tooling-contract.md): the DATA that
// fully describes a self-contained demo mockup, generalized from what the
// gravytanker portal mockup hand-codes in its `const states = {...}`
// literal and `render(key)` function.
//
// A scenario has five stable zones (rail / intake / graph / inspector /
// timeline) plus a `states` map. Each zone's STRUCTURE (headings, rail
// scenario/type lists, intake field labels, an optional graph projection
// path) is declared once under `zones`; each zone's PER-STATE CONTENT
// (decision text, field values, graph state id, metrics/evidence,
// timeline progress) lives under `states.<id>`.

import fs from "node:fs";
import path from "node:path";
import { resolveSlideyRoot } from "./demo-manifest.mjs";

export const SUPPORTED_VERSION = 1;

export function readJSON(absPath) {
  return JSON.parse(fs.readFileSync(absPath, "utf8"));
}

/** Throws a descriptive Error if `raw` isn't a well-formed scenario spec. */
export function validateScenario(raw) {
  if (!raw || typeof raw !== "object") throw new Error("scenario spec must be a JSON object");
  if (raw.version !== SUPPORTED_VERSION) {
    throw new Error(`unsupported scenario spec version: ${JSON.stringify(raw.version)} (expected ${SUPPORTED_VERSION})`);
  }
  if (!raw.title || typeof raw.title !== "string") throw new Error("scenario spec missing required field: title");
  if (!raw.zones || typeof raw.zones !== "object") throw new Error("scenario spec missing required field: zones");
  if (!raw.states || typeof raw.states !== "object" || Array.isArray(raw.states)) {
    throw new Error("scenario spec missing required field: states (object of state id -> state)");
  }
  const stateKeys = Object.keys(raw.states);
  if (stateKeys.length === 0) throw new Error("scenario spec must declare at least one state");
  for (const key of stateKeys) {
    if (!/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(key)) {
      throw new Error(`state id "${key}" is not a valid JS object-literal key (must match [A-Za-z_$][A-Za-z0-9_$]*)`);
    }
  }
  return stateKeys;
}

/**
 * Load and validate a *.mockup.json scenario spec, resolving `zones.graph
 * .projection` (if present) against the spec file's directory.
 */
export function loadScenario(scenarioPath) {
  const absPath = path.resolve(scenarioPath);
  const dir = path.dirname(absPath);
  const raw = readJSON(absPath);
  const stateKeys = validateScenario(raw);

  const projectionRel = raw.zones?.graph?.projection;
  const projectionAbs = projectionRel ? path.resolve(dir, projectionRel) : undefined;

  return { raw, path: absPath, dir, stateKeys, projectionAbs };
}

/**
 * Resolve the slidey checkout root for a scenario using the SAME chain as
 * lib/demo-manifest.mjs's resolveSlideyRoot: the scenario's own `slidey`
 * field (~ expanded, resolved relative to the scenario's directory) ->
 * SLIDEY_SRC env -> ~/code/slidey.
 */
export function resolveScenarioSlideyRoot(scenario) {
  return resolveSlideyRoot({ raw: { slidey: scenario.raw.slidey }, dir: scenario.dir });
}

/**
 * Group state ids by their `group` field (falling back to the state's
 * `rail.active` index resolved through `zones.rail.scenarios[active][0]`
 * when no explicit group is set, or the constant "tour" when there's no
 * rail configured either). Preserves each state's declaration order within
 * its group and returns groups in first-seen order.
 */
export function groupStates(scenario) {
  const { raw, stateKeys } = scenario;
  const railScenarios = raw.zones?.rail?.scenarios || [];
  const groups = new Map();
  for (const key of stateKeys) {
    const state = raw.states[key];
    let group = state.group;
    if (!group) {
      const activeIndex = state.rail?.active;
      const railEntry = typeof activeIndex === "number" ? railScenarios[activeIndex] : undefined;
      group = railEntry ? slugify(railEntry[0]) : "tour";
    }
    if (!groups.has(group)) groups.set(group, []);
    groups.get(group).push(key);
  }
  return groups;
}

export function slugify(value) {
  return String(value || "scenario")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 60) || "scenario";
}
