#!/usr/bin/env node
import { spawn } from "node:child_process";
import fs from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { runDoctor } from "./scripts/demo-doctor.mjs";
import { runRecordTour } from "./scripts/record-tour.mjs";
import { runCreateMockup } from "./scripts/create-mockup.mjs";

const SERVER_INFO = { name: "kitsoki-frontend-mockup", version: "0.1.0" };
const KITSOKI_REPO = process.env.KITSOKI_REPO || process.cwd();
const KITSOKI_AGENT_CMD = process.env.KITSOKI_AGENT_CMD || "kitsoki";
const KITSOKI_AGENT = process.env.KITSOKI_MOCKUP_AGENT || "codex-native";
const DEFAULT_VIEWPORT = { width: 1440, height: 1000 };

let stagehandModulePromise;
let stagehand;
let activeTour;

const viewportSchema = {
  type: "object",
  properties: {
    width: { type: "integer", minimum: 320, maximum: 4096 },
    height: { type: "integer", minimum: 320, maximum: 4096 }
  },
  additionalProperties: false
};

const tools = [
  {
    name: "mockup_status",
    description: "Report whether the local frontend mockup browser session is initialized.",
    inputSchema: { type: "object", properties: {}, additionalProperties: false }
  },
  {
    name: "mockup_navigate",
    description: "Open or reuse the local browser and navigate to a URL for visual review.",
    inputSchema: {
      type: "object",
      properties: {
        url: { type: "string", description: "URL to open." },
        viewport: viewportSchema,
        wait_ms: { type: "integer", minimum: 0, maximum: 10000, description: "Optional settle delay after navigation." }
      },
      required: ["url"],
      additionalProperties: false
    }
  },
  {
    name: "mockup_visual_qa",
    description:
      "Capture a screenshot plus compact visual/design context for LLM visual QA. " +
      "Defaults to a small max_nodes (20) to keep responses small; pass a larger max_nodes for more detail. " +
      "Pass include_context:false for screenshot-only QA (skips building the DOM/heading dump entirely, not just omitting it from the response).",
    inputSchema: {
      type: "object",
      properties: {
        url: { type: "string", description: "Optional URL to navigate before capture." },
        viewport: viewportSchema,
        brief: { type: "string", description: "What the UI is supposed to accomplish." },
        selector: { type: "string", description: "Optional selector to focus the capture and DOM summary." },
        full_page: { type: "boolean", description: "Capture the full page instead of the viewport." },
        include_image: { type: "boolean", description: "Include PNG image content. Defaults to true." },
        include_context: {
          type: "boolean",
          description:
            "Whether to build and include the DOM/layout node dump and headings. Defaults to true for backwards compat. " +
            "Set to false for screenshot-only QA to avoid inflating the response with a large JSON dump."
        },
        max_nodes: {
          type: "integer",
          minimum: 5,
          maximum: 200,
          description: "Maximum DOM/layout nodes to summarize. Defaults to 20 (was 200); raise explicitly if you need more detail."
        },
        wait_ms: { type: "integer", minimum: 0, maximum: 10000, description: "Optional settle delay before capture." }
      },
      additionalProperties: false
    }
  },
  {
    name: "mockup_dom",
    description: "Return a compact DOM, accessibility, and layout representation for wireframe/review work.",
    inputSchema: {
      type: "object",
      properties: {
        url: { type: "string", description: "Optional URL to navigate before reading." },
        viewport: viewportSchema,
        selector: { type: "string", description: "Optional selector to summarize." },
        max_nodes: {
          type: "integer",
          minimum: 5,
          maximum: 300,
          description: "Maximum DOM/layout nodes to summarize. Defaults to 40 (was 80); raise explicitly if you need more detail."
        },
        wait_ms: { type: "integer", minimum: 0, maximum: 10000 }
      },
      additionalProperties: false
    }
  },
  {
    name: "mockup_click",
    description: "Deterministically click a selector on the current mockup page. Does not require an active tour.",
    inputSchema: {
      type: "object",
      properties: {
        selector: { type: "string", description: "CSS selector to click." },
        wait_ms: { type: "integer", minimum: 0, maximum: 30000, description: "Optional delay after clicking." }
      },
      required: ["selector"],
      additionalProperties: false
    }
  },
  {
    name: "mockup_fill",
    description: "Deterministically fill a selector's value on the current mockup page. Does not require an active tour.",
    inputSchema: {
      type: "object",
      properties: {
        selector: { type: "string", description: "CSS selector to fill." },
        value: { type: "string", description: "Text to fill in." },
        wait_ms: { type: "integer", minimum: 0, maximum: 30000, description: "Optional delay after filling." }
      },
      required: ["selector"],
      additionalProperties: false
    }
  },
  {
    name: "mockup_stagehand_observe",
    description: "Use Stagehand to identify visible actions or design-relevant affordances on the current page.",
    inputSchema: {
      type: "object",
      properties: {
        instruction: { type: "string", description: "Optional description of actions or elements to find." }
      },
      additionalProperties: false
    }
  },
  {
    name: "mockup_stagehand_extract",
    description: "Use Stagehand to extract text or structured page information relevant to design review.",
    inputSchema: {
      type: "object",
      properties: {
        instruction: { type: "string", description: "What to extract. Omit for page text." }
      },
      additionalProperties: false
    }
  },
  {
    name: "mockup_stagehand_act",
    description: "Perform a browser action in natural language through Stagehand.",
    inputSchema: {
      type: "object",
      properties: {
        instruction: { type: "string", description: "Action to perform." },
        wait_ms: { type: "integer", minimum: 0, maximum: 10000 }
      },
      required: ["instruction"],
      additionalProperties: false
    }
  },
  {
    name: "mockup_tour_start",
    description: "Start an interactive source-first tour recording for a mockup/demo scenario.",
    inputSchema: {
      type: "object",
      properties: {
        title: { type: "string", description: "Human-readable tour/demo title." },
        slug: { type: "string", description: "Stable file slug. Defaults from title." },
        url: { type: "string", description: "Optional URL to open as the first tour phase." },
        viewport: viewportSchema,
        brief: { type: "string", description: "Scenario or PM-review objective." },
        wait_ms: { type: "integer", minimum: 0, maximum: 10000 }
      },
      required: ["title"],
      additionalProperties: false
    }
  },
  {
    name: "mockup_tour_step",
    description: "Add and optionally perform one deterministic tour beat: caption, spotlight, click, type, wait, snapshot, navigate, or Stagehand action.",
    inputSchema: {
      type: "object",
      properties: {
        kind: {
          type: "string",
          enum: ["caption", "spotlight", "click", "type", "wait", "snapshot", "navigate", "stagehand_act", "phase"]
        },
        title: { type: "string", description: "Narration title or phase name." },
        body: { type: "string", description: "Narration body." },
        selector: { type: "string", description: "DOM selector to spotlight, click, type into, or snapshot." },
        value: { type: "string", description: "Text to type." },
        url: { type: "string", description: "URL for navigate/phase steps." },
        instruction: { type: "string", description: "Natural-language Stagehand action." },
        hold_ms: { type: "integer", minimum: 0, maximum: 30000, description: "How long this beat should hold during replay." },
        wait_ms: { type: "integer", minimum: 0, maximum: 30000, description: "Delay after performing the action now." },
        perform: { type: "boolean", description: "Perform the action in the live browser now. Defaults to true." }
      },
      required: ["kind"],
      additionalProperties: false
    }
  },
  {
    name: "mockup_tour_export",
    description: "Export the active tour source and deterministic replay scaffolding for later demo-video rendering.",
    inputSchema: {
      type: "object",
      properties: {
        output_dir: { type: "string", description: "Output directory. Defaults to .artifacts/frontend-mockup-tours/<slug>." },
        include_screenshots: { type: "boolean", description: "Capture one screenshot per snapshot step during export." }
      },
      additionalProperties: false
    }
  },
  {
    name: "mockup_close",
    description: "Close the current local frontend mockup browser session.",
    inputSchema: { type: "object", properties: {}, additionalProperties: false }
  },
  {
    name: "mockup_demo_doctor",
    description:
      "Run the demo-doctor checks (states, deck paths, freshness, chapters, estimate) against a *.demo.json manifest. " +
      "Does not require an active browser session.",
    inputSchema: {
      type: "object",
      properties: {
        manifest: { type: "string", description: "Absolute path to the *.demo.json manifest." }
      },
      required: ["manifest"],
      additionalProperties: false
    }
  },
  {
    name: "mockup_record_tour",
    description:
      "Run the record-tour closed loop for a *.demo.json manifest: per-cue dwell auto-sizing, tour-set capture via " +
      "slidey, a re-estimate flag check, then a demo-doctor gate. Does not require an active browser session.",
    inputSchema: {
      type: "object",
      properties: {
        manifest: { type: "string", description: "Absolute path to the *.demo.json manifest." }
      },
      required: ["manifest"],
      additionalProperties: false
    }
  },
  {
    name: "mockup_create",
    description:
      "Generate a self-contained mockup HTML from a *.mockup.json scenario spec: five stable zones " +
      "(rail/intake/graph/inspector/timeline), a refreshable states data block, and the " +
      "window.storyboard.setStep(id) contract. Optionally co-emits a starter *.demo.json manifest, one tour per " +
      "state group, and a starter slidey deck so create -> record_tour -> demo_doctor is a closed loop. Does not " +
      "require an active browser session.",
    inputSchema: {
      type: "object",
      properties: {
        scenario: { type: "string", description: "Absolute path to the *.mockup.json scenario spec." },
        out: { type: "string", description: "Absolute path to write the generated mockup HTML." },
        manifest: { type: "boolean", description: "Also emit a starter *.demo.json manifest, tours, and deck." },
        renderer: {
          type: "string",
          description:
            "Absolute path to a graph-projection renderer.js to embed directly, bypassing slidey resolution. " +
            "Omit to auto-resolve a slidey checkout (scenario 'slidey' field -> SLIDEY_SRC env -> ~/code/slidey)."
        }
      },
      required: ["scenario", "out"],
      additionalProperties: false
    }
  }
];

function write(message) {
  process.stdout.write(`${JSON.stringify(message)}\n`);
}

function result(id, value) {
  write({ jsonrpc: "2.0", id, result: value });
}

function error(id, code, message, data) {
  write({ jsonrpc: "2.0", id, error: { code, message, ...(data === undefined ? {} : { data }) } });
}

function textResult(value) {
  const text = typeof value === "string" ? value : JSON.stringify(value, null, 2);
  return { content: [{ type: "text", text }] };
}

function imageResult(value, pngBuffer) {
  const content = [{ type: "text", text: JSON.stringify(value, null, 2) }];
  if (pngBuffer) {
    content.push({ type: "image", data: pngBuffer.toString("base64"), mimeType: "image/png" });
  }
  return { content };
}

function stripInvisible(text) {
  return text.replace(/[\u200b-\u200f\u2060-\u2064\ufeff]/g, "");
}

function slugify(value) {
  return String(value || "mockup-tour")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 80) || "mockup-tour";
}

function extractJSON(text) {
  const clean = stripInvisible(text).trim();
  try {
    return JSON.parse(clean);
  } catch {
    const start = clean.search(/[\[{]/);
    if (start === -1) {
      throw new Error(`kitsoki agent ask did not return JSON: ${clean.slice(0, 500)}`);
    }
    for (let end = clean.length; end > start; end -= 1) {
      try {
        return JSON.parse(clean.slice(start, end));
      } catch {
        // Keep scanning for the final valid JSON suffix.
      }
    }
    throw new Error(`kitsoki agent ask returned unparsable JSON: ${clean.slice(0, 500)}`);
  }
}

function stringifyMessageContent(content) {
  if (typeof content === "string") return content;
  if (!Array.isArray(content)) return JSON.stringify(content);
  return content
    .map((part) => {
      if (part?.type === "text") return part.text || "";
      if (part?.text) return part.text;
      if (part?.image_url || part?.source) return "[image omitted: mockup MCP Stagehand calls use DOM/text context]";
      return JSON.stringify(part);
    })
    .join("\n");
}

function schemaHint(schema) {
  if (!schema) return "";
  if (typeof schema.toJSON === "function") return JSON.stringify(schema.toJSON(), null, 2);
  if (typeof schema.toJSONSchema === "function") return JSON.stringify(schema.toJSONSchema(), null, 2);
  return String(schema);
}

function askKitsoki(prompt) {
  return new Promise((resolve, reject) => {
    const child = spawn(
      KITSOKI_AGENT_CMD,
      ["agent", "ask", "--agent", KITSOKI_AGENT, "--working-dir", KITSOKI_REPO, "--prompt", "-"],
      {
        cwd: KITSOKI_REPO,
        env: { ...process.env, KITSOKI_REPO },
        stdio: ["pipe", "pipe", "pipe"]
      }
    );
    let stdout = "";
    let stderr = "";
    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
    });
    child.stderr.on("data", (chunk) => {
      stderr += chunk;
    });
    child.on("error", reject);
    child.on("close", (code) => {
      if (code !== 0) {
        reject(new Error(`kitsoki agent ask exited ${code}: ${stderr || stdout}`));
        return;
      }
      resolve(stripInvisible(stdout));
    });
    child.stdin.end(prompt);
  });
}

async function loadStagehandModule() {
  if (!stagehandModulePromise) {
    stagehandModulePromise = import("@browserbasehq/stagehand");
  }
  return stagehandModulePromise;
}

async function makeLLMClient(modelName = "openai/gpt-5.5") {
  const { LLMClient } = await loadStagehandModule();
  return new (class KitsokiLLMClient extends LLMClient {
    constructor() {
      super(modelName);
      this.type = "kitsoki";
      this.modelName = modelName;
      this.hasVision = false;
      this.clientOptions = {};
    }

    async createChatCompletion({ options }) {
      const prompt = [
        "You are answering an internal Stagehand browser automation LLM request for frontend mockup review.",
        "Focus on visible UI structure, labels, affordances, and DOM-described behavior.",
        "Return ONLY the requested answer. Do not include markdown fences.",
        options.response_model
          ? `Return ONLY JSON matching this schema:\n${schemaHint(options.response_model.schema)}`
          : "For non-structured requests, return concise plain text.",
        "",
        ...options.messages.map((message) => `${message.role.toUpperCase()}:\n${stringifyMessageContent(message.content)}`)
      ].join("\n\n");
      const stdout = await askKitsoki(prompt);
      if (options.response_model) {
        return {
          data: extractJSON(stdout),
          usage: { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 }
        };
      }
      return {
        id: `kitsoki-mockup-${Date.now()}`,
        object: "chat.completion",
        created: Math.floor(Date.now() / 1000),
        model: modelName,
        choices: [
          {
            index: 0,
            message: { role: "assistant", content: stdout, tool_calls: [] },
            finish_reason: "stop"
          }
        ],
        usage: { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 }
      };
    }
  })();
}

async function ensureStagehand(viewport, forceNew = false) {
  if (stagehand && forceNew) {
    await stagehand.close().catch(() => {});
    stagehand = undefined;
  }
  if (stagehand) {
    if (viewport) await setViewport(viewport);
    return stagehand;
  }
  const { Stagehand } = await loadStagehandModule();
  stagehand = new Stagehand({
    env: "LOCAL",
    llmClient: await makeLLMClient(),
    localBrowserLaunchOptions: {
      headless: process.env.KITSOKI_MOCKUP_HEADLESS !== "0"
    },
    disablePino: true,
    verbose: 0
  });
  await stagehand.init();
  if (viewport) await setViewport(viewport);
  return stagehand;
}

function normalizeViewport(viewport) {
  if (!viewport) return DEFAULT_VIEWPORT;
  return {
    width: Number.isInteger(viewport.width) ? viewport.width : DEFAULT_VIEWPORT.width,
    height: Number.isInteger(viewport.height) ? viewport.height : DEFAULT_VIEWPORT.height
  };
}

async function page(forceNew = false) {
  const sh = await ensureStagehand(undefined, forceNew);
  let [current] = sh.context.pages();
  if (!current) current = await sh.context.newPage();
  return current;
}

// Detects a low-level transport/CDP error surfacing from deep inside Stagehand's
// browser connection, as opposed to a normal Playwright locator/timeout error
// (e.g. "element not found", "Timeout exceeded"). A failed sh.act() call can
// leave the shared page/session in a bad state such that the NEXT plain
// Playwright command on that page throws an opaque CDP error instead of its
// own clean error.
function isTransportError(err) {
  const text = `${err?.message || ""}\n${err?.stack || ""}`;
  if (/-32602/.test(text) && /invalid parameters/i.test(text)) return true;
  if (/@browserbasehq\/stagehand/.test(text)) return true;
  if (/protocol error|cdp session/i.test(text) && !/playwright-core/.test(text)) return true;
  return false;
}

// Runs a Playwright action against the shared session, with one-shot recovery
// from a poisoned session: if the action throws, check whether the page/context
// still looks usable; if it doesn't (or the error itself looks like a transport
// error rather than a normal Playwright failure), force a fresh Stagehand
// session, re-navigate to the same URL, and retry the action exactly once.
// Genuine "element not found"/timeout failures on a healthy session are
// rethrown unchanged, never masked as a session problem.
async function withResilientPage(action) {
  const current = await page();
  let previousUrl;
  try {
    previousUrl = current.url();
  } catch {
    previousUrl = undefined;
  }
  try {
    return await action(current);
  } catch (err) {
    let healthy = true;
    try {
      if (typeof current.isClosed === "function" && current.isClosed()) {
        healthy = false;
      } else {
        await current.title();
      }
    } catch {
      healthy = false;
    }
    if (healthy && !isTransportError(err)) {
      throw err;
    }
    process.stderr.write(
      `[mockup] session looked unhealthy after an action failure; attempting one reconnect+retry. Original error: ${err?.message || err}\n`
    );
    try {
      const fresh = await page(true);
      if (previousUrl && previousUrl !== "about:blank") {
        await fresh.goto(previousUrl, { waitUntil: "domcontentloaded" }).catch(() => {});
      }
      return await action(fresh);
    } catch (retryErr) {
      const wrapped = new Error(
        `mockup browser session appeared to be left in a bad state (often by a prior failed stagehand act() call); ` +
          `attempted a session reconnect and retry, but the retry also failed: ${retryErr?.message || retryErr}. ` +
          `Original error: ${err?.message || err}`
      );
      wrapped.cause = err;
      throw wrapped;
    }
  }
}

async function setViewport(viewport) {
  const current = await page();
  await current.setViewportSize(normalizeViewport(viewport));
}

async function maybeWait(ms) {
  if (ms && ms > 0) {
    await new Promise((resolve) => setTimeout(resolve, ms));
  }
}

async function maybeNavigate(args) {
  const sh = await ensureStagehand(normalizeViewport(args.viewport));
  const current = sh.context.pages()[0] || (await sh.context.newPage());
  if (args.url) {
    const response = await current.goto(args.url, { waitUntil: "domcontentloaded" });
    await maybeWait(args.wait_ms);
    return { navigated: true, status: response?.status?.() ?? null, url: current.url() };
  }
  await maybeWait(args.wait_ms);
  return { navigated: false, status: null, url: current.url() };
}

async function installTourOverlays() {
  const current = await page();
  await current.evaluate((css) => {
    if (!document.getElementById("mockup-tour-style")) {
      const style = document.createElement("style");
      style.id = "mockup-tour-style";
      style.textContent = css;
      document.head.appendChild(style);
    }
    if (!document.getElementById("mockup-tour-caption")) {
      const caption = document.createElement("div");
      caption.id = "mockup-tour-caption";
      document.body.appendChild(caption);
    }
    if (!document.getElementById("mockup-tour-spot")) {
      const spot = document.createElement("div");
      spot.id = "mockup-tour-spot";
      document.body.appendChild(spot);
    }
  },
      `#mockup-tour-caption{position:fixed;top:18px;left:50%;transform:translateX(-50%);` +
      `z-index:2147483000;max-width:min(760px,72vw);background:rgba(2,6,23,.94);color:#e2e8f0;` +
      `border:1px solid #334155;border-left:4px solid #fbbf24;border-radius:10px;padding:14px 20px;` +
      `font:700 20px/1.35 ui-sans-serif,system-ui,sans-serif;box-shadow:0 10px 34px rgba(0,0,0,.45);` +
      `opacity:0;transition:opacity .25s;pointer-events:none}` +
      `#mockup-tour-caption.show{opacity:1}` +
      `#mockup-tour-caption .body{display:block;margin-top:6px;font-weight:400;font-size:14px;color:#cbd5e1}` +
      `#mockup-tour-spot{position:fixed;z-index:2147482999;pointer-events:none;border-radius:10px;border:3px solid #fbbf24;` +
      `box-shadow:0 0 0 3px rgba(251,191,36,.25),0 0 24px rgba(251,191,36,.7);opacity:0;transition:opacity .2s}`
  );
}

async function showCaption(title = "", body = "") {
  await installTourOverlays();
  const current = await page();
  await current.evaluate(
    ({ title, body }) => {
      const el = document.getElementById("mockup-tour-caption");
      if (!el) return;
      el.classList.remove("show");
      el.innerHTML = `${title || ""}${body ? `<span class="body">${body}</span>` : ""}`;
      requestAnimationFrame(() => el.classList.add("show"));
    },
    { title, body }
  );
}

async function showSpotlight(selector) {
  await installTourOverlays();
  const current = await page();
  await current.evaluate((sel) => {
    const box = document.getElementById("mockup-tour-spot");
    if (!box) return;
    if (!sel) {
      box.style.opacity = "0";
      return;
    }
    const target = document.querySelector(sel);
    if (!target) throw new Error(`spotlight selector not found: ${sel}`);
    target.scrollIntoView({ block: "center", inline: "nearest" });
    const rect = target.getBoundingClientRect();
    const pad = 8;
    box.style.left = `${Math.max(0, rect.left - pad)}px`;
    box.style.top = `${Math.max(0, rect.top - pad)}px`;
    box.style.width = `${rect.width + pad * 2}px`;
    box.style.height = `${rect.height + pad * 2}px`;
    box.style.opacity = "1";
  }, selector);
  await maybeWait(300);
}

function ensureTour() {
  if (!activeTour) throw new Error("no active tour; call mockup_tour_start first");
  return activeTour;
}

async function performTourStep(step) {
  switch (step.kind) {
    case "caption":
      await showCaption(step.title || "", step.body || "");
      break;
    case "phase":
      if (step.url) await maybeNavigate({ url: step.url, wait_ms: step.wait_ms });
      await showCaption(step.title || "", step.body || "");
      break;
    case "spotlight":
      if (step.title || step.body) await showCaption(step.title || "", step.body || "");
      await showSpotlight(step.selector);
      break;
    case "click":
      if (!step.selector) throw new Error("click step requires selector");
      await showSpotlight(step.selector);
      await withResilientPage((p) => p.click(step.selector));
      break;
    case "type":
      if (!step.selector) throw new Error("type step requires selector");
      await showSpotlight(step.selector);
      await withResilientPage((p) => p.fill(step.selector, step.value || ""));
      break;
    case "wait":
      break;
    case "snapshot":
      if (step.selector) await showSpotlight(step.selector);
      break;
    case "navigate":
      if (!step.url) throw new Error("navigate step requires url");
      await maybeNavigate({ url: step.url, wait_ms: step.wait_ms });
      break;
    case "stagehand_act": {
      if (!step.instruction) throw new Error("stagehand_act step requires instruction");
      const sh = await ensureStagehand();
      const value = await sh.act(step.instruction);
      if (value && value.success === false) {
        let observed;
        try {
          observed = await sh.observe();
        } catch (obsErr) {
          observed = { error: `observe() also failed: ${obsErr?.message || obsErr}` };
        }
        process.stderr.write(
          `[mockup_tour_step stagehand_act] soft failure for instruction ${JSON.stringify(step.instruction)}: ${JSON.stringify(value)}\n` +
            `Consider falling back to mockup_tour_step({kind:"click", selector}) or mockup_click({selector}) with a deterministic selector. ` +
            `Observed page affordances: ${JSON.stringify(observed)}\n`
        );
      }
      break;
    }
    default:
      throw new Error(`unknown tour step kind: ${step.kind}`);
  }
  await maybeWait(step.wait_ms || step.hold_ms || 0);
}

function tourSource() {
  const tour = ensureTour();
  return {
    schema: "kitsoki.frontend-mockup-tour.v1",
    title: tour.title,
    slug: tour.slug,
    brief: tour.brief,
    created_at: tour.createdAt,
    viewport: tour.viewport,
    start_url: tour.startUrl,
    source: "tools/frontend-mockup-mcp",
    steps: tour.steps
  };
}

function playwrightSpec(sourceFileName) {
  return `import { test, expect } from "@playwright/test";
import fs from "node:fs";

const tour = JSON.parse(fs.readFileSync(new URL("./${sourceFileName}", import.meta.url), "utf8"));

test(tour.title, async ({ page }) => {
  await page.setViewportSize(tour.viewport || { width: 1440, height: 1000 });
  if (tour.start_url) await page.goto(tour.start_url, { waitUntil: "domcontentloaded" });
  await installOverlays(page);
  for (const step of tour.steps) {
    await runStep(page, step);
  }
});

async function runStep(page, step) {
  if (step.kind === "navigate" || step.kind === "phase") {
    if (step.url) await page.goto(step.url, { waitUntil: "domcontentloaded" });
    await installOverlays(page);
  }
  if (step.title || step.body) await caption(page, step.title || "", step.body || "");
  if (step.selector && ["spotlight", "click", "type", "snapshot"].includes(step.kind)) await spotlight(page, step.selector);
  if (step.kind === "click") await page.click(step.selector);
  if (step.kind === "type") await page.fill(step.selector, step.value || "");
  if (step.kind === "stagehand_act") test.skip(true, "Stagehand actions must be converted to deterministic selectors before committed replay");
  await page.waitForTimeout(step.hold_ms || step.wait_ms || 800);
  if (step.kind === "snapshot") await expect(page.locator(step.selector || "body").first()).toBeVisible();
}

async function installOverlays(page) {
  await page.addStyleTag({ content: "#mockup-tour-caption{position:fixed;top:18px;left:50%;transform:translateX(-50%);z-index:2147483000;max-width:min(760px,72vw);background:rgba(2,6,23,.94);color:#e2e8f0;border:1px solid #334155;border-left:4px solid #fbbf24;border-radius:10px;padding:14px 20px;font:700 20px/1.35 ui-sans-serif,system-ui,sans-serif;box-shadow:0 10px 34px rgba(0,0,0,.45);opacity:0;transition:opacity .25s;pointer-events:none}#mockup-tour-caption.show{opacity:1}#mockup-tour-caption .body{display:block;margin-top:6px;font-weight:400;font-size:14px;color:#cbd5e1}#mockup-tour-spot{position:fixed;z-index:2147482999;pointer-events:none;border-radius:10px;border:3px solid #fbbf24;box-shadow:0 0 0 3px rgba(251,191,36,.25),0 0 24px rgba(251,191,36,.7);opacity:0;transition:opacity .2s}" });
  await page.evaluate(() => {
    for (const id of ["mockup-tour-caption", "mockup-tour-spot"]) {
      if (!document.getElementById(id)) {
        const el = document.createElement("div");
        el.id = id;
        document.body.appendChild(el);
      }
    }
  });
}

async function caption(page, title, body) {
  await page.evaluate(({ title, body }) => {
    const el = document.getElementById("mockup-tour-caption");
    el.innerHTML = title + (body ? '<span class="body">' + body + '</span>' : "");
    requestAnimationFrame(() => el.classList.add("show"));
  }, { title, body });
}

async function spotlight(page, selector) {
  await page.evaluate((sel) => {
    const target = document.querySelector(sel);
    if (!target) throw new Error("selector not found: " + sel);
    target.scrollIntoView({ block: "center", inline: "nearest" });
    const rect = target.getBoundingClientRect();
    const box = document.getElementById("mockup-tour-spot");
    const pad = 8;
    box.style.left = Math.max(0, rect.left - pad) + "px";
    box.style.top = Math.max(0, rect.top - pad) + "px";
    box.style.width = rect.width + pad * 2 + "px";
    box.style.height = rect.height + pad * 2 + "px";
    box.style.opacity = "1";
  }, selector);
  await page.waitForTimeout(300);
}
`;
}

function storyboardHTML(source) {
  const escaped = JSON.stringify(source).replace(/</g, "\\u003c");
  return `<!doctype html>
<meta charset="utf-8">
<title>${source.title}</title>
<style>
body{font:14px/1.45 system-ui,sans-serif;margin:0;background:#f6f7f9;color:#172033}
main{max-width:980px;margin:0 auto;padding:32px}
h1{font-size:28px;margin:0 0 8px}.brief{color:#526070;margin-bottom:24px}
.step{background:white;border:1px solid #d7dce4;border-radius:8px;margin:12px 0;padding:16px}
.kind{font:12px ui-monospace,monospace;color:#6b7280;text-transform:uppercase}.title{font-weight:700;margin-top:4px}.body{color:#475569;margin-top:4px}
code{background:#eef1f5;padding:2px 5px;border-radius:4px}
</style>
<main><h1></h1><p class="brief"></p><div id="steps"></div></main>
<script>
const tour=${escaped};
document.querySelector("h1").textContent=tour.title;
document.querySelector(".brief").textContent=tour.brief || "";
document.getElementById("steps").innerHTML=tour.steps.map((s,i)=>'<section class="step"><div class="kind">'+(i+1)+'. '+s.kind+'</div><div class="title">'+(s.title||s.url||s.selector||s.instruction||'')+'</div><div class="body">'+(s.body||'')+'</div>'+(s.selector?'<p><code>'+s.selector+'</code></p>':'')+'</section>').join("");
</script>`;
}

async function collectDesignContext(args = {}, defaultMaxNodes = 80) {
  const current = await page();
  return current.evaluate(
    ({ selector, maxNodes }) => {
      const root = selector ? document.querySelector(selector) : document.body;
      const target = root || document.body;
      const viewport = { width: window.innerWidth, height: window.innerHeight };
      const textOf = (el) => (el.innerText || el.textContent || "").replace(/\s+/g, " ").trim();
      const short = (value, max = 160) => {
        const text = String(value || "").replace(/\s+/g, " ").trim();
        return text.length > max ? `${text.slice(0, max - 1)}...` : text;
      };
      const roleOf = (el) => el.getAttribute("role") || "";
      const nameOf = (el) =>
        el.getAttribute("aria-label") ||
        el.getAttribute("alt") ||
        el.getAttribute("title") ||
        el.getAttribute("placeholder") ||
        short(textOf(el), 80);
      const selectorFor = (el) => {
        if (el.id) return `#${CSS.escape(el.id)}`;
        const testid = el.getAttribute("data-testid") || el.getAttribute("data-test");
        if (testid) return `[data-testid="${testid.replace(/"/g, '\\"')}"]`;
        const parts = [];
        for (let node = el; node && node.nodeType === Node.ELEMENT_NODE && parts.length < 4; node = node.parentElement) {
          let part = node.localName;
          if (node.classList.length) part += `.${Array.from(node.classList).slice(0, 2).map((c) => CSS.escape(c)).join(".")}`;
          const parent = node.parentElement;
          if (parent) {
            const siblings = Array.from(parent.children).filter((child) => child.localName === node.localName);
            if (siblings.length > 1) part += `:nth-of-type(${siblings.indexOf(node) + 1})`;
          }
          parts.unshift(part);
        }
        return parts.join(" > ");
      };
      const isInteresting = (el) => {
        const tag = el.localName;
        return (
          ["a", "button", "input", "textarea", "select", "label", "summary", "dialog", "img", "nav", "main", "header", "footer", "section", "article"].includes(tag) ||
          /^h[1-6]$/.test(tag) ||
          roleOf(el) ||
          el.getAttribute("aria-label") ||
          el.getAttribute("data-testid")
        );
      };
      const nodes = [];
      for (const el of Array.from(target.querySelectorAll("*"))) {
        if (!isInteresting(el)) continue;
        const rect = el.getBoundingClientRect();
        const style = window.getComputedStyle(el);
        if (rect.width <= 0 || rect.height <= 0 || style.visibility === "hidden" || style.display === "none") continue;
        nodes.push({
          tag: el.localName,
          role: roleOf(el) || undefined,
          name: short(nameOf(el), 100) || undefined,
          text: short(textOf(el), 140) || undefined,
          selector: selectorFor(el),
          bbox: {
            x: Math.round(rect.x),
            y: Math.round(rect.y),
            width: Math.round(rect.width),
            height: Math.round(rect.height)
          },
          disabled: el.disabled || el.getAttribute("aria-disabled") === "true" || undefined,
          href: el.localName === "a" ? el.href : undefined,
          type: el.getAttribute("type") || undefined,
          style: {
            fontSize: style.fontSize,
            fontWeight: style.fontWeight,
            color: style.color,
            backgroundColor: style.backgroundColor
          }
        });
        if (nodes.length >= maxNodes) break;
      }
      const bodyStyle = window.getComputedStyle(document.body);
      const headings = Array.from(target.querySelectorAll("h1,h2,h3"))
        .slice(0, 12)
        .map((el) => ({ level: el.localName, text: short(textOf(el), 120), selector: selectorFor(el) }));
      return {
        url: window.location.href,
        title: document.title,
        viewport,
        targetFound: Boolean(root),
        selector: selector || "",
        pageTextPreview: short(textOf(target), 1200),
        visualTokens: {
          bodyBackground: bodyStyle.backgroundColor,
          bodyColor: bodyStyle.color,
          bodyFont: bodyStyle.fontFamily,
          rootWidth: Math.round(target.getBoundingClientRect().width),
          rootHeight: Math.round(target.getBoundingClientRect().height)
        },
        headings,
        nodes
      };
    },
    { selector: args.selector || "", maxNodes: args.max_nodes || defaultMaxNodes }
  );
}

function qaPrompt(brief, context) {
  return [
    "Use the attached screenshot as the primary evidence and the JSON below as semantic backup.",
    "Review only visible product/design quality: layout, hierarchy, spacing, labels, affordances, accessibility cues, responsive fit, and PM-review readiness.",
    "Do not inspect console logs, network requests, frontend source, backend traces, or implementation internals.",
    brief ? `Brief: ${brief}` : "Brief: infer the intended UI from the visible screen.",
    "",
    "Return findings with severity, exact visible evidence, and a concrete design fix.",
    "",
    JSON.stringify(context, null, 2)
  ].join("\n");
}

async function callTool(name, args = {}) {
  switch (name) {
    case "mockup_status":
      return textResult({
        initialized: Boolean(stagehand),
        repo: KITSOKI_REPO,
        agentCommand: KITSOKI_AGENT_CMD,
        agent: KITSOKI_AGENT,
        focus: "frontend mockups, wireframes, DOM summaries, and visual QA"
      });
    case "mockup_navigate": {
      const nav = await maybeNavigate(args);
      return textResult(nav);
    }
    case "mockup_dom": {
      const nav = await maybeNavigate(args);
      const context = await collectDesignContext(args, 40);
      return textResult({ ok: true, ...nav, context });
    }
    case "mockup_visual_qa": {
      const nav = await maybeNavigate(args);
      const current = await page();
      const includeContext = args.include_context !== false;
      const includeImage = args.include_image !== false;
      let clip;
      if (args.selector) {
        clip = await current.evaluate((selector) => {
          const el = document.querySelector(selector);
          if (!el) return undefined;
          const rect = el.getBoundingClientRect();
          return {
            x: Math.max(0, Math.floor(rect.x)),
            y: Math.max(0, Math.floor(rect.y)),
            width: Math.max(1, Math.ceil(rect.width)),
            height: Math.max(1, Math.ceil(rect.height))
          };
        }, args.selector);
      }
      const screenshotOptions = {
        type: "png",
        ...(clip ? { clip } : { fullPage: Boolean(args.full_page) })
      };
      const png = includeImage ? await current.screenshot(screenshotOptions) : undefined;
      if (!includeContext) {
        const minimal = await current.evaluate(
          (selector) => {
            const root = selector ? document.querySelector(selector) : document.body;
            return {
              url: window.location.href,
              title: document.title,
              viewport: { width: window.innerWidth, height: window.innerHeight },
              targetFound: Boolean(root || !selector)
            };
          },
          args.selector || ""
        );
        return imageResult({ ok: true, ...nav, ...minimal }, png);
      }
      const context = await collectDesignContext(args, 20);
      return imageResult(
        {
          ok: true,
          ...nav,
          review_prompt: qaPrompt(args.brief || "", context),
          context
        },
        png
      );
    }
    case "mockup_stagehand_observe": {
      const sh = await ensureStagehand();
      return textResult(await (args.instruction ? sh.observe(args.instruction) : sh.observe()));
    }
    case "mockup_stagehand_extract": {
      const sh = await ensureStagehand();
      return textResult(await (args.instruction ? sh.extract(args.instruction) : sh.extract()));
    }
    case "mockup_stagehand_act": {
      if (!args.instruction) throw new Error("instruction is required");
      const sh = await ensureStagehand();
      const value = await sh.act(args.instruction);
      await maybeWait(args.wait_ms);
      if (value && value.success === false) {
        let observed;
        try {
          observed = await sh.observe();
        } catch (obsErr) {
          observed = { error: `observe() also failed: ${obsErr?.message || obsErr}` };
        }
        process.stderr.write(
          `[mockup_stagehand_act] soft failure for instruction ${JSON.stringify(args.instruction)}: ${JSON.stringify(value)}\n`
        );
        return textResult({
          ...value,
          hint:
            "stagehand act() could not ground this instruction to an action (a soft failure, not a thrown error). " +
            "Consider falling back to the deterministic mockup_click({selector}) or mockup_fill({selector,value}) tools, " +
            "or mockup_tour_step({kind:'click'|'type', selector}), using one of the observed selectors below.",
          observed
        });
      }
      return textResult(value);
    }
    case "mockup_click": {
      if (!args.selector) throw new Error("selector is required");
      await withResilientPage((p) => p.click(args.selector));
      await maybeWait(args.wait_ms);
      return textResult({ ok: true, clicked: args.selector });
    }
    case "mockup_fill": {
      if (!args.selector) throw new Error("selector is required");
      await withResilientPage((p) => p.fill(args.selector, args.value || ""));
      await maybeWait(args.wait_ms);
      return textResult({ ok: true, filled: args.selector });
    }
    case "mockup_tour_start": {
      const viewport = normalizeViewport(args.viewport);
      const nav = await maybeNavigate({ url: args.url, viewport, wait_ms: args.wait_ms });
      activeTour = {
        title: args.title,
        slug: slugify(args.slug || args.title),
        brief: args.brief || "",
        viewport,
        startUrl: args.url || nav.url || "",
        createdAt: new Date().toISOString(),
        steps: []
      };
      await installTourOverlays();
      if (args.title || args.brief) await showCaption(args.title, args.brief || "");
      if (args.url) {
        activeTour.steps.push({ kind: "navigate", url: args.url, hold_ms: args.wait_ms || 800 });
      }
      return textResult({ ok: true, tour: tourSource() });
    }
    case "mockup_tour_step": {
      const tour = ensureTour();
      const step = {
        id: `step-${String(tour.steps.length + 1).padStart(3, "0")}`,
        kind: args.kind,
        title: args.title || "",
        body: args.body || "",
        selector: args.selector || "",
        value: args.value || "",
        url: args.url || "",
        instruction: args.instruction || "",
        hold_ms: args.hold_ms || args.wait_ms || 1200,
        wait_ms: args.wait_ms || 0,
        recorded_at: new Date().toISOString()
      };
      if (args.perform !== false) await performTourStep(step);
      tour.steps.push(step);
      return textResult({ ok: true, added: step, step_count: tour.steps.length });
    }
    case "mockup_tour_export": {
      const source = tourSource();
      const outputDir = path.resolve(
        KITSOKI_REPO,
        args.output_dir || path.join(".artifacts", "frontend-mockup-tours", source.slug)
      );
      await fs.mkdir(outputDir, { recursive: true });
      const sourceFile = `${source.slug}.tour.json`;
      const sourcePath = path.join(outputDir, sourceFile);
      const specPath = path.join(outputDir, `${source.slug}.replay.spec.ts`);
      const storyboardPath = path.join(outputDir, "storyboard.html");
      await fs.writeFile(sourcePath, `${JSON.stringify(source, null, 2)}\n`);
      await fs.writeFile(specPath, playwrightSpec(sourceFile));
      await fs.writeFile(storyboardPath, storyboardHTML(source));
      return textResult({
        ok: true,
        source: sourcePath,
        replay_spec: specPath,
        storyboard: storyboardPath,
        next: [
          "review the tour JSON and replace any stagehand_act steps with deterministic selectors before committing",
          "run the replay spec with Playwright to render MP4/GIF artifacts under .artifacts"
        ]
      });
    }
    case "mockup_close":
      if (stagehand) {
        await stagehand.close();
        stagehand = undefined;
      }
      activeTour = undefined;
      return textResult({ closed: true });
    case "mockup_demo_doctor": {
      if (!args.manifest) throw new Error("manifest is required");
      return textResult(runDoctor(path.resolve(args.manifest)));
    }
    case "mockup_record_tour": {
      if (!args.manifest) throw new Error("manifest is required");
      try {
        return textResult(await runRecordTour(path.resolve(args.manifest)));
      } catch (err) {
        if (err.doctorReport) {
          return textResult({ ok: false, error: err.message, doctor: err.doctorReport });
        }
        throw err;
      }
    }
    case "mockup_create": {
      if (!args.scenario) throw new Error("scenario is required");
      if (!args.out) throw new Error("out is required");
      return textResult(
        runCreateMockup({
          scenarioPath: path.resolve(args.scenario),
          outPath: path.resolve(args.out),
          manifest: Boolean(args.manifest),
          rendererPath: args.renderer ? path.resolve(args.renderer) : undefined
        })
      );
    }
    default:
      throw new Error(`unknown tool: ${name}`);
  }
}

let buffer = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (chunk) => {
  buffer += chunk;
  for (;;) {
    const index = buffer.indexOf("\n");
    if (index === -1) break;
    const line = buffer.slice(0, index).replace(/\r$/, "");
    buffer = buffer.slice(index + 1);
    if (!line.trim()) continue;
    void handleLine(line);
  }
});

async function handleLine(line) {
  let message;
  try {
    message = JSON.parse(line);
  } catch (err) {
    error(null, -32700, `parse error: ${err.message}`);
    return;
  }
  if (!message.id && message.method?.startsWith("notifications/")) return;
  try {
    switch (message.method) {
      case "initialize":
        result(message.id, {
          protocolVersion: message.params?.protocolVersion || "2025-03-26",
          capabilities: { tools: {} },
          serverInfo: SERVER_INFO
        });
        break;
      case "ping":
        result(message.id, {});
        break;
      case "tools/list":
        result(message.id, { tools });
        break;
      case "tools/call":
        result(message.id, await callTool(message.params?.name, message.params?.arguments || {}));
        break;
      default:
        error(message.id, -32601, `method not found: ${message.method}`);
    }
  } catch (err) {
    result(message.id, {
      isError: true,
      content: [{ type: "text", text: err?.stack || err?.message || String(err) }]
    });
  }
}

process.on("SIGINT", async () => {
  if (stagehand) await stagehand.close().catch(() => {});
  process.exit(130);
});
