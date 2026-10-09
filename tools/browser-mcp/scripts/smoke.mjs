#!/usr/bin/env node
// No-LLM smoke test for the deterministic primitives: spawns the real
// server.mjs over stdio (a real headless Stagehand/Playwright browser
// launches, but observe/act are never called, so no LLM call happens),
// drives it through the MCP wire protocol against the local fixture page,
// and asserts the primitive tool surface actually works end to end.
import { fileURLToPath } from "node:url";
import path from "node:path";
import { startClient, assertOk } from "../test/mcp-stdio-client.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const serverPath = path.join(here, "..", "server.mjs");
const fixturePath = path.join(here, "..", "test", "fixtures", "fixture.html");
const fixtureUrl = `file://${fixturePath}`;

async function main() {
  const client = startClient({ serverPath });

  const init = await client.initialize();
  assertOk(init.result?.serverInfo?.name === "kitsoki-browser-mcp", "initialize returns server info");

  const list = await client.send("tools/list", {});
  const names = (list.result?.tools || []).map((t) => t.name);
  for (const expected of [
    "browser_navigate",
    "browser_click",
    "browser_fill",
    "browser_scroll",
    "browser_press",
    "browser_snapshot",
    "browser_find",
    "browser_batch",
    "observe",
    "act",
    "tour_start",
    "tour_step",
    "tour_export",
    "tour_replay"
  ]) {
    assertOk(names.includes(expected), `tools/list includes ${expected}`);
  }

  const nav = await client.callTool("browser_navigate", { url: fixtureUrl });
  assertOk(nav.url === fixtureUrl, "browser_navigate opens the fixture page (no LLM call)");

  const snap = await client.callTool("browser_snapshot", {});
  assertOk(Array.isArray(snap.refs) && snap.refs.length > 0, "browser_snapshot captures interactive elements");
  assertOk(snap.refs.some((r) => r.testid === "save-btn"), "browser_snapshot finds the save button by testid");

  const found = await client.callTool("browser_find", { query: "save" });
  assertOk(found.refs.length === 1 && found.refs[0].testid === "save-btn", "browser_find narrows to the matching subset");

  const clicked = await client.callTool("browser_click", { testid: "save-btn" });
  assertOk(clicked.clicked === true && clicked.strategy === "testid", "browser_click resolves by testid and clicks");

  const filled = await client.callTool("browser_fill", { testid: "name-input", value: "kitsoki" });
  assertOk(filled.filled === true, "browser_fill resolves and fills");

  const scrolled = await client.callTool("browser_scroll", { testid: "panel-btn" });
  assertOk(scrolled.scrolled === true && scrolled.mode === "into-view", "browser_scroll scrolls a resolved target into view");

  const badAnchor = await client.callTool("browser_click", { testid: "does-not-exist" }).catch((err) => err);
  assertOk(badAnchor instanceof Error, "browser_click on a missing anchor fails loudly, not silently");

  const batch = await client.callTool("browser_batch", {
    ops: [
      { tool: "browser_navigate", args: { url: fixtureUrl } },
      { tool: "browser_click", args: { testid: "save-btn" } }
    ]
  });
  assertOk(batch.results.length === 2 && batch.results.every((r) => r.ok), "browser_batch runs multiple primitive ops in one call");

  await client.callTool("browser_close", {});
  client.child.kill();
  console.log("\nbrowser-mcp smoke: ALL PRIMITIVES PASSED (no LLM calls made)");
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
