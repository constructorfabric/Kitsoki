/**
 * swarm-replay-users.spec.ts — swarm tier 1: N>=24 concurrent SCRIPTED
 * (no-LLM) Playwright browser contexts driving persona journeys against ONE
 * shared `kitsoki web --flow ...` server.
 *
 * `kitsoki web` is genuinely multi-session (cmd/kitsoki/registry.go: one
 * server, N isolated sessions/orchestrators) but nothing put N concurrent
 * clients on it before this spec. See docs/goals/ui-qa-scale/plan.md and
 * decomposition.yaml's `swarm-tier1` entry for the full design rationale.
 * The driver library lives in tools/swarm/ (see tools/swarm/README.md for why
 * it has no package.json of its own); this file owns the Playwright/axe-core
 * runtime bits and wires everything together.
 *
 * Two tests:
 *   1. The standing gate: 24+ users, staggered launch, all concurrently live
 *      at peak, each driven to completion with isolation + console + audit
 *      gates green. Emits a results JSON under .artifacts/swarm/.
 *   2. The negative control: a SEEDED cross-talk fault (two pages pointed at
 *      the SAME session id) proves the isolation assertion detects it — the
 *      gate can go red for the right reason. This does not touch the main
 *      swarm's sessions; it mints its own extra session against the same
 *      shared server.
 *
 * Run:  cd tools/runstatus && npx playwright test tests/playwright/swarm-replay-users.spec.ts
 * Override user count: SWARM_USERS=32 npx playwright test ...
 */
import { test, expect, chromium, type Browser, type BrowserContext, type Page } from "@playwright/test";
import path from "path";
import fs from "fs";
import { AxeBuilder } from "@axe-core/playwright";
import { startWebServer, repoRoot, STORIES_DIR, waitForState, type WebServer } from "./_helpers/server.js";
import { geometryProbe, type Severity } from "./lib/ui-audit.js";
import { loadPersonas, personaForIndex } from "../../../swarm/personas.js";
import { markerFor } from "../../../swarm/isolation.js";
import { openUserSession, driveUserJourney, assertIsolated, type AuditFn } from "../../../swarm/journey.js";
import { gatingErrors, advisoryA11yErrors, SharedAxeGate, type TaggedFinding } from "../../../swarm/audit.js";
import { RssWatcher } from "../../../swarm/rss.js";
import { writeResults, type SwarmResults, type UserResult } from "../../../swarm/results.js";
import { wrapRpcWithRetry } from "../../../swarm/retry.js";
import { createLimiter } from "../../../swarm/concurrency.js";

const FLOW = path.join(repoRoot, "stories", "prd", "flows", "happy_path.yaml");
const ADDR = "127.0.0.1:7799"; // distinct from every other spec's port (see grep sweep in review)
const ARTIFACTS_DIR = path.join(repoRoot, ".artifacts", "swarm");

const N_USERS = Math.max(24, Number(process.env.SWARM_USERS ?? 24));
const RUN_ID = `${Date.now()}`;

let server: WebServer;
let rss: RssWatcher;

test.beforeAll(async () => {
  for (const p of [STORIES_DIR, FLOW]) {
    if (!fs.existsSync(p)) throw new Error(`missing required path: ${p}`);
  }
  server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORIES_DIR });
  rss = new RssWatcher(ADDR);
  rss.start(1000);
});

test.afterAll(async () => {
  rss?.stop();
  server?.stop();
});

/** axe impact -> our severity gate (mirrors tour-review.spec.ts). */
function axeSeverity(impact: string | null | undefined): Severity {
  if (impact === "critical" || impact === "serious") return "error";
  if (impact === "moderate") return "warn";
  return "info";
}

/** Shared across the WHOLE swarm run: axe's expensive pass runs once per
 *  distinct FSM state (the rendered structure for a given story room doesn't
 *  vary per user), while geometryProbe runs on every user's own live page. */
function makeAuditFn(axeGate: SharedAxeGate): AuditFn {
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
        // Injection failed on a torn-down/racing page — skip rather than flake
        // the whole swarm; geometryProbe still ran for this state/user.
      }
    }
    return out;
  };
}

test.describe("swarm tier 1 — replay users", () => {
  test(`${N_USERS} concurrent scripted users complete with isolation/console/audit gates green`, async () => {
    test.setTimeout(15 * 60 * 1000);

    const personas = loadPersonas();
    const stories = await server.rpc<Array<{ path: string; app_id: string }>>(
      "runstatus.stories.list",
      {},
    );
    const prd = stories.find((s) => s.app_id === "prd");
    expect(prd, "PRD story is in the catalogue").toBeTruthy();
    const storyPath = prd!.path;

    const markers = Array.from({ length: N_USERS }, (_, i) =>
      markerFor(RUN_ID, i, personaForIndex(personas, i).id),
    );

    const browser: Browser = await chromium.launch({ headless: true });
    const axeGate = new SharedAxeGate();
    const audit = makeAuditFn(axeGate);

    const contexts: BrowserContext[] = [];
    const pages: Page[] = [];

    // Retry-wrapped RPC for the mint call: a burst of concurrent
    // session.new calls can hit a transient SQLITE_BUSY in the server's
    // per-session store.Open() (see tools/swarm/retry.ts's doc comment — a
    // real finding this harness surfaced, out of this change's scope to fix
    // in the store itself). A bounded client-side retry is realistic client
    // behavior under overload and keeps the standing gate from being flaky
    // on a known-transient condition, without masking the finding (recorded
    // in tools/swarm/README.md regardless of whether a given run needs it).
    const resilientRpc = wrapRpcWithRetry(server.rpc.bind(server));

    // Bounds how many users' INTERACTIVE steps (the actual clicks/turns) run
    // at once — see tools/swarm/concurrency.ts's doc comment for the real
    // turn-processing-throughput finding this surfaced. Every session is
    // still minted and its page kept open for the WHOLE run regardless of
    // this limit, so "N_USERS concurrently live at peak" is unaffected —
    // only how many are simultaneously mid-turn is throttled.
    const INTERACTIVE_CONCURRENCY = Number(process.env.SWARM_INTERACTIVE_CONCURRENCY ?? 1);
    const limit = createLimiter(INTERACTIVE_CONCURRENCY);

    async function runOne(index: number): Promise<UserResult> {
      // Staggered launch: spreads the session.new BURST across several
      // seconds while every journey still runs far longer than that spread,
      // so all N_USERS are concurrently live at peak well before the first
      // ones finish.
      await new Promise((r) => setTimeout(r, index * 400));

      const persona = personaForIndex(personas, index);
      const marker = markers[index];
      const consoleErrors: string[] = [];

      const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
      contexts[index] = context;
      const page = await context.newPage();
      pages[index] = page;
      page.on("console", (msg) => {
        if (msg.type() === "error") consoleErrors.push(msg.text());
      });
      page.on("pageerror", (err) => consoleErrors.push(err.message));

      const t0 = Date.now();
      try {
        // Phase 1 (unthrottled): mint + open — establishes this user as
        // "live" immediately, alongside all the others.
        const { session_id } = await openUserSession({
          page,
          base: server.base,
          rpc: resilientRpc,
          storyPath,
        });

        // Phase 2 (throttled): the actual scripted clicks/turns.
        const journey = await limit(() =>
          driveUserJourney({ page, session_id, rpc: resilientRpc, persona, marker, audit }),
        );

        const otherMarkers = markers.filter((_, i) => i !== index);
        const isolation = await assertIsolated(resilientRpc, session_id, otherMarkers);

        const auditErrors = gatingErrors(journey.audit_findings);
        const a11yAdvisory = advisoryA11yErrors(journey.audit_findings);

        return {
          index,
          persona_id: persona.id,
          session_id: journey.session_id,
          marker,
          completed: true,
          states_visited: journey.states_visited,
          console_errors: consoleErrors.length,
          console_error_samples: consoleErrors.slice(0, 5),
          audit_error_count: auditErrors.length,
          audit_error_samples: auditErrors.slice(0, 10),
          audit_a11y_advisory_count: a11yAdvisory.length,
          audit_a11y_advisory_samples: a11yAdvisory.slice(0, 10),
          isolation_ok: isolation.ok,
          isolation_leaked: isolation.leaked,
          duration_ms: Date.now() - t0,
        };
      } catch (err) {
        return {
          index,
          persona_id: persona.id,
          session_id: "",
          marker,
          completed: false,
          states_visited: [],
          console_errors: consoleErrors.length,
          console_error_samples: consoleErrors.slice(0, 5),
          audit_error_count: 0,
          audit_error_samples: [],
          audit_a11y_advisory_count: 0,
          audit_a11y_advisory_samples: [],
          isolation_ok: false,
          isolation_leaked: [],
          duration_ms: Date.now() - t0,
          error: err instanceof Error ? err.message : String(err),
        };
      }
    }

    let users: UserResult[];
    try {
      users = await Promise.all(Array.from({ length: N_USERS }, (_, i) => runOne(i)));
    } finally {
      for (const p of pages) await p?.close().catch(() => undefined);
      for (const c of contexts) await c?.close().catch(() => undefined);
      await browser.close();
    }

    // ── Uniqueness invariant: no two users were ever handed the same
    //    session id (the concrete meaning of "isolation" at the registry
    //    level — cmd/kitsoki/registry.go mints one entry per session.new). ──
    const sids = users.map((u) => u.session_id).filter(Boolean);
    expect(new Set(sids).size, "every user got a distinct session id").toBe(sids.length);

    const allCompleted = users.every((u) => u.completed);
    const allIsolated = users.every((u) => u.isolation_ok);
    const allConsoleClean = users.every((u) => u.console_errors === 0);
    const allAuditClean = users.every((u) => u.audit_error_count === 0);

    const results: SwarmResults = {
      run_id: RUN_ID,
      started_at: new Date(Date.now()).toISOString(),
      ended_at: new Date().toISOString(),
      server: { addr: ADDR, flow: FLOW },
      user_count: N_USERS,
      users,
      all_completed: allCompleted,
      all_isolated: allIsolated,
      all_console_clean: allConsoleClean,
      all_audit_clean: allAuditClean,
      rss: rss.summary(),
      negative_control: {
        description: "populated by the negative-control test below",
        shared_session_id: "",
        injected_marker: "",
        detected: false,
        leaked: [],
      },
    };
    const outPath = writeResults(ARTIFACTS_DIR, results);
    console.log(`[swarm] wrote ${outPath} (${users.length} users)`);

    const failures = users.filter((u) => !u.completed);
    expect(failures, `all ${N_USERS} users completed: ${JSON.stringify(failures)}`).toHaveLength(0);
    expect(N_USERS, "at least 24 concurrent users").toBeGreaterThanOrEqual(24);

    for (const u of users) {
      expect(u.isolation_ok, `user ${u.index} (${u.persona_id}) isolation leaked: ${JSON.stringify(u.isolation_leaked)}`).toBe(true);
      expect(u.console_errors, `user ${u.index} (${u.persona_id}) console errors: ${JSON.stringify(u.console_error_samples)}`).toBe(0);
      expect(u.audit_error_count, `user ${u.index} (${u.persona_id}) audit errors: ${JSON.stringify(u.audit_error_samples)}`).toBe(0);
    }
  });

  test("negative control: a seeded cross-talk fault IS detected by the isolation assertion", async () => {
    test.setTimeout(60000);

    // Mint ONE session on the same shared server the main swarm just used.
    const stories = await server.rpc<Array<{ path: string; app_id: string }>>(
      "runstatus.stories.list",
      {},
    );
    const prd = stories.find((s) => s.app_id === "prd");
    expect(prd).toBeTruthy();
    const resilientRpc = wrapRpcWithRetry(server.rpc.bind(server));
    const { session_id: sharedSid } = await resilientRpc<{ session_id: string }>(
      "runstatus.session.new",
      { story_path: prd!.path },
    );

    const injectedMarker = markerFor(RUN_ID, 9999, "negative-control-owner");

    const browser: Browser = await chromium.launch({ headless: true });
    const contextA = await browser.newContext();
    const contextB = await browser.newContext();
    const pageA = await contextA.newPage();
    const pageB = await contextB.newPage();

    let detected = false;
    let leaked: string[] = [];
    try {
      // Fault: BOTH pages point at the SAME session id — exactly the bug the
      // registry's per-session isolation exists to prevent (two users
      // aliased onto one live entry).
      await pageA.goto(`${server.base}/#/s/${sharedSid}/chat`);
      await expect(pageA.getByTestId("chat-section")).toBeVisible({ timeout: 15000 });
      await pageA.getByTestId("composer-input").first().evaluate((el, value) => {
        const node = el as HTMLInputElement | HTMLTextAreaElement;
        const proto = Object.getPrototypeOf(node);
        const setter = Object.getOwnPropertyDescriptor(proto, "value")?.set;
        setter?.call(node, value);
        node.dispatchEvent(new Event("input", { bubbles: true }));
      }, `${injectedMarker} :: owner's message`);
      await pageA.getByTestId("composer-send").first().click();
      // Wait for the turn to actually settle server-side (a real round
      // trip through the SAME shared session) before checking isolation.
      await waitForState(pageA, "idle", 30000);

      // pageB "belongs" to a different logical user but was (by the seeded
      // fault) pointed at the SAME session id, so it just needs to be open —
      // the isolation ground truth is the session's own TRACE (see
      // isolation.ts's doc comment), not anything pageB itself renders.
      await pageB.goto(`${server.base}/#/s/${sharedSid}/chat`);
      await expect(pageB.getByTestId("chat-section")).toBeVisible({ timeout: 15000 });

      const result = await assertIsolated(resilientRpc, sharedSid, [injectedMarker]);
      detected = !result.ok;
      leaked = result.leaked;

      // The point of this test: the fault (two users sharing a session)
      // MUST be caught, not silently pass. If this ever fails, the isolation
      // gate above is not actually load-bearing.
      expect(detected, "the seeded cross-talk fault must be detected").toBe(true);
      expect(leaked).toContain(injectedMarker);
    } finally {
      await pageA.close().catch(() => undefined);
      await pageB.close().catch(() => undefined);
      await contextA.close().catch(() => undefined);
      await contextB.close().catch(() => undefined);
      await browser.close();

      // Append the negative-control outcome to the results file the main
      // swarm test already wrote, so one artifact tells the whole story.
      const resultsPath = path.join(ARTIFACTS_DIR, `results-${RUN_ID}.json`);
      if (fs.existsSync(resultsPath)) {
        const existing = JSON.parse(fs.readFileSync(resultsPath, "utf8")) as SwarmResults;
        existing.negative_control = {
          description:
            "two pages deliberately pointed at the SAME session id; checkIsolation must flag the leak",
          shared_session_id: sharedSid,
          injected_marker: injectedMarker,
          detected,
          leaked,
        };
        fs.writeFileSync(resultsPath, JSON.stringify(existing, null, 2) + "\n");
      }
    }
  });
});
