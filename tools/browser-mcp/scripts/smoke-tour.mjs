#!/usr/bin/env node
// P3 acceptance test: author a tour over a fixture app headlessly (no LLM —
// tour_step's target validation/enrichment is pure DOM inspection), replay
// it no-LLM against the SAME fixture and assert every step passes with
// zero heals, then replay the identical exported tour JSON against a
// MUTATED fixture (the save button's data-testid was renamed but its text
// stayed "Save") and assert exactly one audited heal event
// (failedAnchor: testid -> matchedAnchor: text).
import { fileURLToPath } from "node:url";
import path from "node:path";
import { startClient, assertOk } from "../test/mcp-stdio-client.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const serverPath = path.join(here, "..", "server.mjs");
const fixtureUrl = `file://${path.join(here, "..", "test", "fixtures", "tour-fixture.html")}`;
const mutatedFixtureUrl = `file://${path.join(here, "..", "test", "fixtures", "tour-fixture-mutated.html")}`;

async function main() {
  const client = startClient({ serverPath });
  await client.initialize();

  // --- author -------------------------------------------------------
  await client.callTool("browser_navigate", { url: fixtureUrl });
  await client.callTool("tour_start", { id: "how-to-save", origin: "file://" });

  // Author with ONLY testid — enrichment should fill in `text` from the
  // live DOM, so the exported bundle carries both fields even though the
  // author supplied one.
  const appended = await client.callTool("tour_step", {
    id: "step-save",
    kind: "act",
    target: { testid: "save-btn" },
    popover: { title: "Save your work", body: "Click Save", side: "bottom" },
    act: { kind: "click" },
    policy: "confirm"
  });
  assertOk(appended.appended === true && appended.stepCount === 1, "tour_step appends a validated step (no LLM call)");

  const tour = await client.callTool("tour_export", {});
  assertOk(tour.version === 2 && tour.id === "how-to-save", "tour_export returns tour format v2 JSON");
  assertOk(tour.steps[0].target.testid === "save-btn", "exported step keeps the author-supplied testid");
  assertOk(tour.steps[0].target.text === "Save", "tour_step enriched the bundle with the live DOM's text");
  assertOk(tour.steps[0].target.role === undefined, "enrichment never invents fields the DOM doesn't carry");

  // --- replay against the SAME fixture: zero heals -------------------
  await client.callTool("browser_navigate", { url: fixtureUrl });
  const replaySame = await client.callTool("tour_replay", { tour });
  assertOk(replaySame.passed === true, "tour_replay passes every step against the authored fixture");
  assertOk(replaySame.heals.length === 0, "tour_replay against an unchanged fixture emits zero heals");
  assertOk(replaySame.results[0].strategy === "testid", "unchanged fixture resolves via the primary (testid) strategy");

  // --- replay against the MUTATED fixture: one audited heal ----------
  await client.callTool("browser_navigate", { url: mutatedFixtureUrl });
  const replayMutated = await client.callTool("tour_replay", { tour });
  assertOk(replayMutated.passed === true, "tour_replay still passes via the healed anchor");
  assertOk(replayMutated.heals.length === 1, "tour_replay against the mutated fixture emits exactly one heal event");
  const heal = replayMutated.heals[0];
  assertOk(heal.stepId === "step-save", "heal event is attributed to the correct step");
  assertOk(heal.failedAnchor === "testid", "heal event records the failed (primary) anchor strategy");
  assertOk(heal.matchedAnchor === "text", "heal event records the strategy that actually resolved");
  assertOk(typeof heal.confidence === "number" && heal.confidence > 0, "heal event carries a confidence score");

  await client.callTool("browser_close", {});
  client.child.kill();
  console.log("\nbrowser-mcp tour smoke: AUTHOR + REPLAY + HEAL ALL PASSED (no LLM calls made)");
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
