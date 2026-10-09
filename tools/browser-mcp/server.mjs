#!/usr/bin/env node
// tools/browser-mcp: the converged, keyless browser-automation MCP server —
// deterministic primitives (no LLM) plus Stagehand-grounded observe/act
// (via the kitsoki harness). Authoring half of the browser-MCP + tour work
// (.context/2026-07-12-browser-mcp-tour-implementation-brief.md, P2).
// Ports tools/stagehand-mcp's harness bridge and headless-LOCAL launch
// verbatim; tour_* authoring tools land in P3.
//
// Built on the official MCP TypeScript SDK (stdio transport) rather than a
// hand-rolled JSON-RPC loop. Each primitive is a plain async handler
// function so browser_batch can dispatch to the SAME code the individual
// tool registrations call, rather than reaching into McpServer internals.
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { z } from "zod";
import { makeKitsokiLLMClient } from "./lib/kitsoki-llm-client.mjs";
import { resolveAnchor, AnchorResolutionError } from "./lib/anchors.mjs";
import { captureSnapshot, filterSnapshot, resolveRef, DEFAULT_SNAPSHOT_CAP } from "./lib/snapshot.mjs";
import { tourStart, tourStep, tourExport, tourReplay } from "./lib/tour.mjs";

const KITSOKI_REPO = process.env.KITSOKI_REPO || process.cwd();
const KITSOKI_AGENT_CMD = process.env.KITSOKI_AGENT_CMD || "kitsoki";
const KITSOKI_AGENT = process.env.KITSOKI_BROWSER_MCP_AGENT || "codex-native";
const HEADLESS = process.env.KITSOKI_BROWSER_MCP_HEADLESS !== "0";
const ORIGIN_ALLOWLIST = (process.env.KITSOKI_BROWSER_MCP_ORIGIN_ALLOWLIST || "")
  .split(",")
  .map((s) => s.trim())
  .filter(Boolean);

let stagehandModulePromise;
let stagehand;
let lastSnapshot = null;

async function loadStagehandModule() {
  if (!stagehandModulePromise) stagehandModulePromise = import("@browserbasehq/stagehand");
  return stagehandModulePromise;
}

async function ensureStagehand() {
  if (stagehand) return stagehand;
  const { Stagehand, LLMClient } = await loadStagehandModule();
  stagehand = new Stagehand({
    env: "LOCAL",
    llmClient: makeKitsokiLLMClient(LLMClient, {
      repo: KITSOKI_REPO,
      agentCmd: KITSOKI_AGENT_CMD,
      agent: KITSOKI_AGENT
    }),
    localBrowserLaunchOptions: { headless: HEADLESS },
    disablePino: true,
    verbose: 0
  });
  await stagehand.init();
  return stagehand;
}

async function currentPage() {
  const sh = await ensureStagehand();
  let [page] = sh.context.pages();
  if (!page) page = await sh.context.newPage();
  return page;
}

// Exact-match origin allowlist enforcement (research anti-pattern: no
// evasion features, headless is honest, origin binding is exact-match).
// Empty allowlist means "no restriction" (local fixture / dev use).
function assertOriginAllowed(url) {
  if (ORIGIN_ALLOWLIST.length === 0) return;
  const origin = new URL(url).origin;
  if (!ORIGIN_ALLOWLIST.includes(origin)) {
    throw new Error(`origin ${origin} is not in KITSOKI_BROWSER_MCP_ORIGIN_ALLOWLIST (${ORIGIN_ALLOWLIST.join(", ")})`);
  }
}

function textResult(value) {
  const text = typeof value === "string" ? value : JSON.stringify(value, null, 2);
  return { content: [{ type: "text", text }] };
}

function errorResult(err) {
  const extra = err instanceof AnchorResolutionError ? { anchor: err.anchor, attempts: err.attempts } : {};
  return {
    isError: true,
    content: [{ type: "text", text: JSON.stringify({ error: err?.message || String(err), ...extra }) }]
  };
}

const anchorShape = {
  role: z.string().optional(),
  name: z.string().optional(),
  testid: z.string().optional(),
  text: z.string().optional(),
  css: z.string().optional(),
  ancestor: z.string().optional(),
  ref: z
    .string()
    .optional()
    .describe('A ref from a prior browser_snapshot/browser_find call, e.g. "e3". Takes precedence over the other anchor fields when present.')
};

// Resolves an anchor-or-ref to {locator, strategy, selector}. `selector` is
// the CSS string backing the locator (a data-kitsoki-anchor-id/ref
// attribute) — primitives that need page.evaluate() because Stagehand's
// wrapped Locator omits a method (scrollIntoView, focus-for-press) rebuild
// their own querySelector from it rather than needing a second resolve.
async function resolveAnchorOrRef(page, anchor) {
  if (anchor.ref) {
    const selector = `[data-kitsoki-ref="${anchor.ref}"]`;
    const locator = await resolveRef(page, anchor.ref);
    return { locator, strategy: "ref", selector };
  }
  return resolveAnchor(page, anchor);
}

// --- primitive handlers (the single source of truth; both the individual
// tool registrations below and browser_batch call these) -----------------
//
// Stagehand wraps Playwright's Page/Locator with a reduced API (see
// lib/anchors.mjs's header comment): no Locator.press()/.scrollIntoView(),
// and page.click(selector)/page.type(selector, ...) throw a -32602 "invalid
// parameters" transport error (the exact landmine
// tools/frontend-mockup-mcp hit — see mockup_click in its server.mjs).
// Every primitive here goes through page.locator(...).click()/.fill() (the
// methods the wrapper DOES keep) or page.evaluate() for anything else.

async function handleNavigate({ url }) {
  assertOriginAllowed(url);
  const page = await currentPage();
  const response = await page.goto(url, { waitUntil: "domcontentloaded" });
  lastSnapshot = null;
  return { url: page.url(), status: response?.status?.() ?? null };
}

async function handleClick(anchor) {
  const page = await currentPage();
  const { locator, strategy } = await resolveAnchorOrRef(page, anchor);
  await locator.click();
  return { clicked: true, strategy };
}

async function handleFill({ value, ...anchor }) {
  const page = await currentPage();
  const { locator, strategy } = await resolveAnchorOrRef(page, anchor);
  await locator.fill(value);
  return { filled: true, strategy };
}

async function handleScroll({ deltaX = 0, deltaY = 0, ...anchor }) {
  const page = await currentPage();
  const hasAnchor = Object.values(anchor).some(Boolean);
  if (hasAnchor) {
    const { strategy, selector } = await resolveAnchorOrRef(page, anchor);
    await page.evaluate((sel) => {
      const el = document.querySelector(sel);
      if (el) el.scrollIntoView({ block: "center", inline: "nearest" });
    }, selector);
    return { scrolled: true, mode: "into-view", strategy };
  }
  await page.evaluate(({ dx, dy }) => window.scrollBy(dx, dy), { dx: deltaX, dy: deltaY });
  return { scrolled: true, mode: "delta", deltaX, deltaY };
}

async function handlePress({ key, ...anchor }) {
  const page = await currentPage();
  const hasAnchor = Object.values(anchor).some(Boolean);
  if (hasAnchor) {
    // The wrapper has no Locator.focus()/.press(); clicking focuses the
    // element (a real click, which is why text inputs/buttons both work),
    // then the key press goes through the page-level keyPress kitsoki
    // confirmed works standalone.
    const { locator, strategy } = await resolveAnchorOrRef(page, anchor);
    await locator.click();
    await page.keyPress(key);
    return { pressed: key, strategy };
  }
  await page.keyPress(key);
  return { pressed: key, strategy: "page" };
}

const PRIMITIVE_HANDLERS = {
  browser_navigate: handleNavigate,
  browser_click: handleClick,
  browser_fill: handleFill,
  browser_scroll: handleScroll,
  browser_press: handlePress
};

function asTool(handler) {
  return async (args) => {
    try {
      return textResult(await handler(args));
    } catch (err) {
      return errorResult(err);
    }
  };
}

const server = new McpServer({ name: "kitsoki-browser-mcp", version: "0.1.0" });

// --- deterministic primitives (no LLM) ---------------------------------

server.registerTool(
  "browser_navigate",
  {
    description: "Navigate the shared headless page to a URL. Deterministic, no LLM call.",
    inputSchema: { url: z.string().describe("URL to open.") }
  },
  asTool(handleNavigate)
);

server.registerTool(
  "browser_click",
  {
    description:
      "Click the element resolved from a ranked anchor bundle (role+name -> testid -> text -> css -> ancestor), or from a prior snapshot ref. Deterministic, no LLM call.",
    inputSchema: anchorShape
  },
  asTool(handleClick)
);

server.registerTool(
  "browser_fill",
  {
    description: "Fill the element resolved from a ranked anchor bundle (or snapshot ref) with a value. Deterministic, no LLM call.",
    inputSchema: { ...anchorShape, value: z.string() }
  },
  asTool(handleFill)
);

server.registerTool(
  "browser_scroll",
  {
    description: "Scroll the page (or a resolved target element into view) by an optional delta. Deterministic, no LLM call.",
    inputSchema: { ...anchorShape, deltaX: z.number().optional(), deltaY: z.number().optional() }
  },
  asTool(handleScroll)
);

server.registerTool(
  "browser_press",
  {
    description: "Press a keyboard key on the page (or a resolved target element). Deterministic, no LLM call.",
    inputSchema: { ...anchorShape, key: z.string() }
  },
  asTool(handlePress)
);

server.registerTool(
  "browser_snapshot",
  {
    description:
      "Capture a size-capped, interactive-only, ref-based snapshot of the current page (or a css-scoped subtree). Deterministic, no LLM call.",
    inputSchema: {
      cap: z.number().int().positive().max(200).optional(),
      within: z.string().optional().describe("Optional CSS selector scoping the snapshot to a subtree.")
    }
  },
  asTool(async ({ cap = DEFAULT_SNAPSHOT_CAP, within }) => {
    const page = await currentPage();
    lastSnapshot = await captureSnapshot(page, { cap, withinSelector: within });
    return lastSnapshot;
  })
);

server.registerTool(
  "browser_find",
  {
    description:
      "Search the most recent browser_snapshot for elements matching a query (case-insensitive substring over name/text/testid/role/tag). Returns only the matching subset, never the full page. Deterministic, no LLM call.",
    inputSchema: { query: z.string() }
  },
  asTool(async ({ query }) => {
    if (!lastSnapshot) {
      const page = await currentPage();
      lastSnapshot = await captureSnapshot(page);
    }
    return filterSnapshot(lastSnapshot, query);
  })
);

const batchOpSchema = z.object({
  tool: z.enum(Object.keys(PRIMITIVE_HANDLERS)),
  args: z.record(z.string(), z.unknown()).optional()
});

server.registerTool(
  "browser_batch",
  {
    description:
      "Run N deterministic primitive ops in one call (navigate/click/fill/scroll/press). Stops at the first failure unless continueOnError is set.",
    inputSchema: { ops: z.array(batchOpSchema).min(1).max(50), continueOnError: z.boolean().optional() }
  },
  async ({ ops, continueOnError = false }) => {
    const results = [];
    for (const op of ops) {
      try {
        results.push({ tool: op.tool, ok: true, result: await PRIMITIVE_HANDLERS[op.tool](op.args || {}) });
      } catch (err) {
        results.push({ tool: op.tool, ok: false, error: err?.message || String(err) });
        if (!continueOnError) break;
      }
    }
    return textResult({ results });
  }
);

// --- LLM-grounded (Stagehand via the harness) ---------------------------

server.registerTool(
  "observe",
  {
    description:
      "Observe candidate actions on the current page via Stagehand (LLM-grounded through the kitsoki harness). Returns serializable Action[] candidates that act() can replay with zero further LLM calls.",
    inputSchema: { instruction: z.string().optional() }
  },
  asTool(async ({ instruction } = {}) => {
    const sh = await ensureStagehand();
    return instruction ? sh.observe(instruction) : sh.observe();
  })
);

server.registerTool(
  "act",
  {
    description:
      "Perform a browser action. Given a natural-language instruction (string), this is LLM-grounded via the harness. Given an Action object from a prior observe() call, it replays deterministically with ZERO further LLM calls.",
    inputSchema: {
      instruction: z.string().optional(),
      action: z.record(z.string(), z.unknown()).optional().describe("A single Action object as returned by observe().")
    }
  },
  asTool(async ({ instruction, action }) => {
    if (!instruction && !action) throw new Error("act requires either instruction or action");
    const sh = await ensureStagehand();
    return sh.act(action || instruction);
  })
);

// --- tour authoring + replay (tour format v2) ----------------------------
//
// The browser-mcp package's tour tools produce and consume v2 JSON
// (schemas/tour-v2.schema.json, internal/tour/manifest_v2.go,
// tools/runstatus/src/tour/types-v2.ts) directly — tour_export's output is
// valid input to tour_replay, to internal/tour's v2 loader, and to the
// player (P4).

const targetBundleShape = {
  role: z.string().optional(),
  name: z.string().optional(),
  testid: z.string().optional(),
  text: z.string().optional(),
  css: z.string().optional(),
  ancestor: z.string().optional()
};

const popoverShape = {
  title: z.string().optional(),
  body: z.string().optional(),
  side: z.enum(["top", "bottom", "left", "right", "center"]).optional(),
  align: z.enum(["start", "center", "end"]).optional()
};

server.registerTool(
  "tour_start",
  {
    description: "Begin authoring a new tour format v2 tour. Call tour_step to add steps, then tour_export.",
    inputSchema: { id: z.string(), origin: z.string().optional() }
  },
  asTool(async (args) => tourStart(args))
);

server.registerTool(
  "tour_step",
  {
    description:
      "Append one step to the active tour, validating (and enriching from the live DOM) its target anchor. No LLM call.",
    inputSchema: {
      id: z.string(),
      route: z.string().optional(),
      target: z.object(targetBundleShape).optional(),
      popover: z.object(popoverShape).optional(),
      kind: z.enum(["highlight", "gate", "act", "navigate"]),
      advanceOn: z.object({ event: z.enum(["click", "input", "route", "submit"]) }).optional(),
      act: z.object({ kind: z.enum(["click", "fill", "scroll", "press"]), value: z.string().optional() }).optional(),
      policy: z.enum(["watch", "confirm", "auto"]).optional()
    }
  },
  asTool(async (step) => {
    const page = await currentPage();
    return tourStep(page, step);
  })
);

server.registerTool(
  "tour_export",
  { description: "Return the active tour as tour format v2 JSON." },
  asTool(async () => tourExport())
);

server.registerTool(
  "tour_replay",
  {
    description:
      "Deterministically replay a v2 tour against the current page: resolves every step's target (with anchor healing), performs act steps, and reports pass/fail + any HealEvent per step. No LLM call.",
    inputSchema: {
      tour: z.record(z.string(), z.unknown()).describe("A tour format v2 JSON document, as returned by tour_export.")
    }
  },
  asTool(async ({ tour }) => {
    const page = await currentPage();
    return tourReplay(page, tour);
  })
);

// --- lifecycle ------------------------------------------------------------

server.registerTool("browser_close", { description: "Close the shared browser session." }, async () => {
  if (stagehand) {
    await stagehand.close();
    stagehand = undefined;
  }
  lastSnapshot = null;
  return textResult({ closed: true });
});

process.on("SIGINT", async () => {
  if (stagehand) await stagehand.close().catch(() => {});
  process.exit(130);
});

const transport = new StdioServerTransport();
await server.connect(transport);
