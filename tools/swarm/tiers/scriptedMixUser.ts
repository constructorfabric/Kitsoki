/**
 * scriptedMixUser.ts — a tier-1-STYLE (scripted, zero free text, zero LLM)
 * user driven against `stories/off-ramp-demo`, used ONLY to prove tier-2's
 * coexistence acceptance criterion: "cassette-agent users ... coexisting
 * with tier-1 users on the same server."
 *
 * Tier 1's OWN driver (tools/swarm/journey.ts) is PRD-story-specific
 * (composer-select intents, `idle`/`search`/`clarifying` states) and can't
 * run against off-ramp-demo. Rather than force tier 1's exact driver onto an
 * unrelated story, this reuses off-ramp-demo's own MENU affordance — clicking
 * `browse` needs no recording entry, no harness routing, and no free text at
 * all (see `stories/off-ramp-demo/assets/recording.yaml`'s doc comment: "Menu
 * picks ... are EXPLICIT intents ... they need no recording entry and no
 * harness") — which is exactly tier 1's defining property, just expressed
 * against a different story so it can share ONE `kitsoki web` server process
 * (one runtimeBase, one harness config) with tier-2's cassette-agent users
 * driving the SAME story's free-text off-ramp.
 */
import type { Page } from "@playwright/test";
import { waitForState } from "../../runstatus/tests/playwright/_helpers/server.js";
import type { TaggedFinding } from "../audit.js";

export type RpcFn = <T>(method: string, params: Record<string, unknown>) => Promise<T>;
export type AuditFn = (page: Page, state: string) => Promise<TaggedFinding[]>;

const STEP_TIMEOUT_MS = 60000;

export interface ScriptedMixResult {
  session_id: string;
  states_visited: string[];
  audit_findings: TaggedFinding[];
  duration_ms: number;
}

/** Mint + open (unthrottled, mirrors tier 1's phase-1/phase-2 split) then
 *  drive the ONE scripted transition (`browse` -> `catalogue`) with no free
 *  text and no interpreter routing involved at all. */
export async function driveScriptedMixUser(opts: {
  page: Page;
  base: string;
  rpc: RpcFn;
  storyPath: string;
  audit: AuditFn;
}): Promise<ScriptedMixResult> {
  const { page, base, rpc, storyPath, audit } = opts;
  const t0 = Date.now();
  const states: string[] = [];
  const findings: TaggedFinding[] = [];

  const { session_id } = await rpc<{ session_id: string }>("runstatus.session.new", {
    story_path: storyPath,
  });
  await page.goto(`${base}/#/s/${session_id}/chat`);
  await page.getByTestId("chat-section").first().waitFor({ state: "visible", timeout: STEP_TIMEOUT_MS });
  await waitForState(page, "desk", STEP_TIMEOUT_MS);
  states.push("desk");
  findings.push(...(await audit(page, "desk")));

  const browseBtn = page.getByTestId("intent-btn-browse").first();
  await browseBtn.waitFor({ state: "visible", timeout: STEP_TIMEOUT_MS });
  await browseBtn.click();
  await waitForState(page, "catalogue", STEP_TIMEOUT_MS);
  states.push("catalogue");
  findings.push(...(await audit(page, "catalogue")));

  return {
    session_id,
    states_visited: states,
    audit_findings: findings,
    duration_ms: Date.now() - t0,
  };
}
