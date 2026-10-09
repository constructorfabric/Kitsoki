/**
 * swarm-cassette-users.spec.ts — the STANDING CI gate for swarm tiers 2-3
 * (docs/goals/ui-qa-scale/decomposition.yaml's `swarm-tiers23`).
 *
 * Two independent test groups, both zero-LLM:
 *
 *   1. "tier 2 coexists with tier 1 on one server" — mints BOTH a handful of
 *      tier-1-STYLE scripted users (tools/swarm/tiers/scriptedMixUser.ts:
 *      explicit `browse` intent click, no free text, no interpreter routing)
 *      and a handful of tier-2 CASSETTE-AGENT users
 *      (tools/swarm/tiers/tier2.ts: a real free-text sentence routed through
 *      `--harness replay --recording ...`, answered via a `--host-cassette`)
 *      against ONE shared `kitsoki web` server process — the literal
 *      coexistence the acceptance criteria ask for. Reuses
 *      `stories/off-ramp-demo`'s existing recording/cassette fixtures
 *      (already proven live in off-ramp-video.spec.ts) rather than
 *      hand-authoring new ones; tier2.ts derives a small per-run recording
 *      (one entry per user, so each user's own isolation marker still routes
 *      deterministically — see tier2.ts's doc comment for why a marker can't
 *      just be prepended to the shipped recording's one entry).
 *
 *   2. "tier 3 stubbed dispatch contract" — exercises
 *      tools/swarm/tiers/explorer.ts's `dispatchExplorers` with an injected
 *      STUB `runExplorer` (no browser, no subprocess, no LLM call) to prove
 *      the GATING/BUDGET/JOURNALING contract: refuses without
 *      `--live-explorers`-equivalent opt-in, hard-clamps to <=3 explorers
 *      even when asked for more, and aggregates each stub outcome's
 *      findings/blockers/cost. This is what stands in CI for tier 3's
 *      contract — the REAL live wiring lives in
 *      tools/swarm/tiers/liveExplorerCli.ts and is manual-only (see
 *      tools/swarm/README.md).
 *
 * Run:  cd tools/runstatus && npx playwright test tests/playwright/swarm-cassette-users.spec.ts
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import { AxeBuilder } from "@axe-core/playwright";
import { startWebServer, repoRoot, STORIES_DIR, waitForState, type WebServer } from "./_helpers/server.js";
import { geometryProbe, type Severity } from "./lib/ui-audit.js";
import { loadPersonas, personaForIndex, type Persona } from "../../../swarm/personas.js";
import { markerFor } from "../../../swarm/isolation.js";
import { gatingErrors, advisoryA11yErrors, SharedAxeGate, type TaggedFinding } from "../../../swarm/audit.js";
import {
  OFF_RAMP_STORY_DIR,
  OFF_RAMP_HOST_CASSETTE,
  buildTier2RecordingAuto,
  makeTier2ScratchDir,
  openCassetteUserSession,
  driveCassetteUserJourney,
  assertCassetteIsolated,
  type AuditFn as CassetteAuditFn,
} from "../../../swarm/tiers/tier2.js";
import { driveScriptedMixUser, type AuditFn as ScriptedAuditFn } from "../../../swarm/tiers/scriptedMixUser.js";
import {
  dispatchExplorers,
  resolveExplorerBudget,
  assertLiveExplorersAllowed,
  LiveExplorersNotAllowedError,
  MAX_LIVE_EXPLORERS,
  type ExplorerOutcome,
  type ExplorerLens,
} from "../../../swarm/tiers/explorer.js";

const ADDR = "127.0.0.1:7804"; // distinct from every other spec's port (see swarm-replay-users.spec.ts's note)
const RUN_ID = `${Date.now()}`;
const N_SCRIPTED = 4; // tier-1-style scripted users sharing this server
const N_CASSETTE = 4; // tier-2 cassette-agent users sharing this server

/** axe impact -> our severity gate (mirrors swarm-replay-users.spec.ts). */
function axeSeverity(impact: string | null | undefined): Severity {
  if (impact === "critical" || impact === "serious") return "error";
  if (impact === "moderate") return "warn";
  return "info";
}

function makeAuditFn(axeGate: SharedAxeGate): CassetteAuditFn & ScriptedAuditFn {
  return async (page: Page, state: string): Promise<TaggedFinding[]> => {
    const out: TaggedFinding[] = [];
    for (const f of await page.evaluate(geometryProbe)) {
      out.push({ ...f, source: "geometry" });
    }
    if (axeGate.claim(state)) {
      try {
        const results = await new AxeBuilder({ page })
          .disableRules(["region", "landmark-one-main", "page-has-heading-one"])
          .analyze();
        for (const v of results.violations) {
          const node = v.nodes[0];
          const sel = node?.target?.join(" ") || "";
          out.push({
            check: `a11y:${v.id}`,
            severity: axeSeverity(v.impact),
            selector: sel,
            path: sel,
            html: (node?.html || "").replace(/\s+/g, " ").trim().slice(0, 300),
            styles: {},
            rect: { x: 0, y: 0, w: 0, h: 0 },
            text: (node?.html || "").replace(/\s+/g, " ").trim().slice(0, 80),
            detail: v.help,
            source: "a11y",
          });
        }
      } catch {
        // Injection failed on a torn-down/racing page — skip rather than flake.
      }
    }
    return out;
  };
}

test.describe("swarm tier 2 — cassette-agent users coexisting with tier-1-style users", () => {
  let server: WebServer;
  let scratchDir: string;

  test.beforeAll(async () => {
    for (const p of [STORIES_DIR, OFF_RAMP_STORY_DIR, OFF_RAMP_HOST_CASSETTE]) {
      if (!fs.existsSync(p)) throw new Error(`missing required path: ${p}`);
    }
    scratchDir = makeTier2ScratchDir(RUN_ID);
    const markers = Array.from({ length: N_CASSETTE }, (_, i) => markerFor(RUN_ID, i, `cassette-${i}`));
    // buildTier2RecordingAuto (task 3.2, docs/proposals/scenario-foundry.md):
    // when SWARM_FIXTURE names a mined scenario-IR file/dir, the cassette
    // users' off-menu question is sourced from that (SWARM_PERSONA_MIX
    // selects/orders which scenarios), ordered/selected deterministically;
    // unset (the default here) reproduces the original hardcoded
    // OFF_MENU_QUESTION byte-for-byte, so this spec's existing assertions are
    // unaffected unless a run explicitly opts in via the env vars.
    const recordingPath = buildTier2RecordingAuto(markers, scratchDir);

    server = await startWebServer({
      addr: ADDR,
      storiesDir: STORIES_DIR,
      harness: "replay",
      recording: recordingPath,
      hostCassette: OFF_RAMP_HOST_CASSETTE,
    });
  });

  test.afterAll(async () => {
    server?.stop();
    if (scratchDir) fs.rmSync(scratchDir, { recursive: true, force: true });
  });

  test(`${N_SCRIPTED} scripted + ${N_CASSETTE} cassette-agent users complete on ONE shared server`, async () => {
    test.setTimeout(5 * 60 * 1000);

    const personas = loadPersonas();
    const stories = await server.rpc<Array<{ path: string; app_id: string }>>("runstatus.stories.list", {});
    const offRamp = stories.find((s) => s.app_id === "off-ramp-demo");
    expect(offRamp, "off-ramp-demo story is in the catalogue").toBeTruthy();
    const storyPath = offRamp!.path;

    const cassetteMarkers = Array.from({ length: N_CASSETTE }, (_, i) => markerFor(RUN_ID, i, `cassette-${i}`));

    const browser: Browser = await chromium.launch({ headless: true });
    const axeGate = new SharedAxeGate();
    const audit = makeAuditFn(axeGate);

    const contexts: BrowserContext[] = [];
    const pages: Page[] = [];

    async function newPage(): Promise<Page> {
      const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
      contexts.push(context);
      const page = await context.newPage();
      pages.push(page);
      return page;
    }

    interface Outcome {
      kind: "scripted" | "cassette";
      index: number;
      completed: boolean;
      states_visited: string[];
      audit_error_count: number;
      isolation_ok: boolean;
      isolation_leaked: string[];
      console_errors: number;
      error?: string;
    }

    async function runScripted(index: number): Promise<Outcome> {
      const page = await newPage();
      const consoleErrors: string[] = [];
      page.on("console", (msg) => {
        if (msg.type() === "error") consoleErrors.push(msg.text());
      });
      page.on("pageerror", (err) => consoleErrors.push(err.message));
      try {
        const result = await driveScriptedMixUser({ page, base: server.base, rpc: server.rpc.bind(server), storyPath, audit });
        const auditErrors = gatingErrors(result.audit_findings);
        return {
          kind: "scripted",
          index,
          completed: true,
          states_visited: result.states_visited,
          audit_error_count: auditErrors.length,
          isolation_ok: true,
          isolation_leaked: [],
          console_errors: consoleErrors.length,
        };
      } catch (err) {
        return {
          kind: "scripted",
          index,
          completed: false,
          states_visited: [],
          audit_error_count: 0,
          isolation_ok: false,
          isolation_leaked: [],
          console_errors: consoleErrors.length,
          error: err instanceof Error ? err.message : String(err),
        };
      }
    }

    async function runCassette(index: number): Promise<Outcome> {
      const page = await newPage();
      const consoleErrors: string[] = [];
      page.on("console", (msg) => {
        if (msg.type() === "error") consoleErrors.push(msg.text());
      });
      page.on("pageerror", (err) => consoleErrors.push(err.message));
      const marker = cassetteMarkers[index];
      try {
        const { session_id } = await openCassetteUserSession({ page, base: server.base, rpc: server.rpc.bind(server), storyPath });
        const journey = await driveCassetteUserJourney({ page, session_id, marker, audit });
        const otherMarkers = cassetteMarkers.filter((_, i) => i !== index);
        const isolation = await assertCassetteIsolated(server.rpc.bind(server), session_id, otherMarkers);
        const auditErrors = gatingErrors(journey.audit_findings);
        return {
          kind: "cassette",
          index,
          completed: true,
          states_visited: journey.states_visited,
          audit_error_count: auditErrors.length,
          isolation_ok: isolation.ok,
          isolation_leaked: isolation.leaked,
          console_errors: consoleErrors.length,
        };
      } catch (err) {
        return {
          kind: "cassette",
          index,
          completed: false,
          states_visited: [],
          audit_error_count: 0,
          isolation_ok: false,
          isolation_leaked: [],
          console_errors: consoleErrors.length,
          error: err instanceof Error ? err.message : String(err),
        };
      }
    }

    let outcomes: Outcome[];
    try {
      outcomes = await Promise.all([
        ...Array.from({ length: N_SCRIPTED }, (_, i) => runScripted(i)),
        ...Array.from({ length: N_CASSETTE }, (_, i) => runCassette(i)),
      ]);
    } finally {
      for (const p of pages) await p.close().catch(() => undefined);
      for (const c of contexts) await c.close().catch(() => undefined);
      await browser.close();
    }

    const failures = outcomes.filter((o) => !o.completed);
    expect(failures, `all users completed: ${JSON.stringify(failures)}`).toHaveLength(0);

    for (const o of outcomes) {
      expect(o.console_errors, `${o.kind} user ${o.index} console errors`).toBe(0);
      expect(o.audit_error_count, `${o.kind} user ${o.index} audit errors`).toBe(0);
      if (o.kind === "cassette") {
        expect(o.isolation_ok, `cassette user ${o.index} isolation leaked: ${JSON.stringify(o.isolation_leaked)}`).toBe(true);
      }
    }

    const scriptedOutcomes = outcomes.filter((o) => o.kind === "scripted");
    const cassetteOutcomes = outcomes.filter((o) => o.kind === "cassette");
    expect(scriptedOutcomes).toHaveLength(N_SCRIPTED);
    expect(cassetteOutcomes).toHaveLength(N_CASSETTE);
    for (const o of scriptedOutcomes) {
      expect(o.states_visited).toContain("catalogue");
    }
    for (const o of cassetteOutcomes) {
      expect(o.states_visited).toContain("desk-offramp-answered");
      expect(o.states_visited).toContain("catalogue");
    }
  });
});

test.describe("swarm tier 3 — stubbed live-explorer dispatch contract (no LLM, no browser)", () => {
  const personas: Persona[] = loadPersonas();
  const lensFor = (): ExplorerLens => ({
    starting_surface: "stub",
    first_question: "stub",
    evidence_emphasis: "stub",
    escalation_trigger: "stub",
    finding_bias: "stub",
  });

  test("refuses to dispatch without an explicit live opt-in", async () => {
    expect(() => assertLiveExplorersAllowed({ liveExplorers: false })).toThrow(LiveExplorersNotAllowedError);
    // @ts-expect-error — proving a non-boolean truthy value still refuses;
    // the gate requires `=== true`, not merely truthy.
    expect(() => assertLiveExplorersAllowed({ liveExplorers: "yes" })).toThrow(LiveExplorersNotAllowedError);

    let dispatchCalled = false;
    await expect(
      dispatchExplorers({
        liveExplorers: false,
        requestedCount: 3,
        personas,
        serverBase: "http://127.0.0.1:0",
        runDir: "/dev/null",
        lensFor,
        runExplorer: async () => {
          dispatchCalled = true;
          throw new Error("must never be called");
        },
      }),
    ).rejects.toThrow(LiveExplorersNotAllowedError);
    expect(dispatchCalled, "runExplorer must never be invoked without the live opt-in").toBe(false);
  });

  test("hard-clamps the explorer budget to <= 3 even when more are requested", () => {
    expect(resolveExplorerBudget(3)).toBe(3);
    expect(resolveExplorerBudget(999)).toBe(MAX_LIVE_EXPLORERS);
    expect(resolveExplorerBudget(-5)).toBe(0);
    expect(resolveExplorerBudget(Number.NaN)).toBe(0);
  });

  test("dispatches at most MAX_LIVE_EXPLORERS via a STUB runner and journals findings/cost", async () => {
    const dispatchedPersonaIds: string[] = [];
    const stubOutcome = (personaId: string): ExplorerOutcome => ({
      persona_id: personaId,
      ok: true,
      findings_recorded: 2,
      blockers_recorded: 0,
      cost_usd: 0.05,
    });

    const result = await dispatchExplorers({
      liveExplorers: true, // explicit opt-in, exactly as liveExplorerCli.ts would thread from --live-explorers
      requestedCount: 999, // deliberately over-request to prove the clamp end-to-end
      personas,
      serverBase: "http://127.0.0.1:0", // never dialed — the stub runner ignores it
      runDir: "/dev/null", // never touched — the stub runner never shells out
      lensFor,
      runExplorer: async (briefing) => {
        dispatchedPersonaIds.push(briefing.persona.id);
        return stubOutcome(briefing.persona.id);
      },
    });

    expect(result.dispatched).toBe(MAX_LIVE_EXPLORERS);
    expect(dispatchedPersonaIds).toHaveLength(MAX_LIVE_EXPLORERS);
    expect(result.all_ok).toBe(true);
    expect(result.total_findings_recorded).toBe(2 * MAX_LIVE_EXPLORERS);
    expect(result.total_blockers_recorded).toBe(0);
    expect(result.total_cost_usd).not.toBeNull();
    expect(result.total_cost_usd!).toBeCloseTo(0.05 * MAX_LIVE_EXPLORERS, 6);
  });

  test("reports total cost as null (not 0) when no stub outcome reported one", async () => {
    const result = await dispatchExplorers({
      liveExplorers: true,
      requestedCount: 1,
      personas,
      serverBase: "http://127.0.0.1:0",
      runDir: "/dev/null",
      lensFor,
      runExplorer: async (briefing) => ({
        persona_id: briefing.persona.id,
        ok: true,
        findings_recorded: 0,
        blockers_recorded: 0,
        // cost_usd deliberately omitted
      }),
    });
    expect(result.total_cost_usd).toBeNull();
  });

  test("a failing explorer is reflected in all_ok without throwing the dispatch", async () => {
    const result = await dispatchExplorers({
      liveExplorers: true,
      requestedCount: 2,
      personas,
      serverBase: "http://127.0.0.1:0",
      runDir: "/dev/null",
      lensFor,
      runExplorer: async (briefing, index) => ({
        persona_id: briefing.persona.id,
        ok: index !== 0,
        findings_recorded: 0,
        blockers_recorded: index === 0 ? 1 : 0,
        error: index === 0 ? "seeded stub failure" : undefined,
      }),
    });
    expect(result.all_ok).toBe(false);
    expect(result.total_blockers_recorded).toBe(1);
  });
});
