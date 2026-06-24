/**
 * thinking-bubble.spec.ts — regression for "the thinking is stuck at the
 * bottom and tool calls keep pushing it down" in the InteractiveView's live
 * streaming bubble (data-testid="thinking-bubble").
 *
 * The bubble must render the turn-stream feed in ARRIVAL order — a thought
 * stays ABOVE the tool calls that follow it, each thought marked 🧠 like the
 * TUI — not thoughts and tools in two separate buckets (the old shape, which
 * re-ordered every tool above the thinking).
 *
 * The cassette/no-LLM posture never emits turn-stream frames (replay skips
 * the claude subprocess entirely), so the page's fetch is monkeypatched to
 * serve a paced SSE body replicating a real live session: thought →
 * ToolSearch → Bash → thought → Read → done. No LLM, fully deterministic.
 * Screenshots land in .artifacts/thinking-bubble/ for visual review.
 */
import { test, expect, chromium } from "@playwright/test";
import { fileURLToPath } from "url";
import path from "path";
import { startWebServer, makeShot } from "./_helpers/server.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(__dirname, "../../../..");
const FLOW = path.join(repoRoot, "stories", "prd", "flows", "happy_path.yaml");
const ARTIFACTS = path.join(repoRoot, ".artifacts", "thinking-bubble");

test("streaming bubble interleaves thinking and tools with 🧠", async () => {
  test.setTimeout(90000);
  const server = await startWebServer({ addr: "127.0.0.1:7747", flow: FLOW });
  const browser = await chromium.launch();
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  const shot = makeShot(ARTIFACTS);
  try {
    const stories = await server.rpc<Array<{ path: string; app_id: string }>>(
      "runstatus.stories.list",
      {},
    );
    const prd = stories.find((s) => s.app_id === "prd")!;
    const { session_id: sid } = await server.rpc<{ session_id: string }>(
      "runstatus.session.new",
      { story_path: prd.path },
    );

    await page.addInitScript(() => {
      const orig = window.fetch.bind(window);
      // @ts-expect-error patching for the test
      window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = typeof input === "string" ? input : (input as Request).url ?? String(input);
        if (!url.includes("rpc/turn-stream")) return orig(input as never, init);
        const frames: Array<{ delay: number; data: Record<string, unknown> }> = [
          { delay: 600, data: { type: "delta", text: "I'll scan the proposal docs for the tamagotchi spec first." } },
          { delay: 900, data: { type: "tool", tool: "ToolSearch", preview: "select:WebSearch" } },
          { delay: 900, data: { type: "tool", tool: "Bash", preview: 'find /Users/brad/code/Kitsoki -type f -name "*.md" | grep …' } },
          { delay: 1200, data: { type: "delta", text: "An ephemeral animated tamagotchi-style pet appears in the kitsoki web UI whenever the LLM is processing, giving developers a playful visual companion during wait states. The goal is to reduce perceived wait-time friction and keep the developer experience alive and delightful." } },
          { delay: 1200, data: { type: "tool", tool: "Read", preview: "docs/proposals/tamagotchi.md" } },
          { delay: 6000, data: { type: "done", result: { mode: "ok", state: "clarifying", view: "Noted — drafting now.", turn_number: 2 } } },
        ];
        const enc = new TextEncoder();
        const stream = new ReadableStream({
          async start(controller) {
            for (const f of frames) {
              await new Promise((r) => setTimeout(r, f.delay));
              controller.enqueue(enc.encode(`data: ${JSON.stringify(f.data)}\n\n`));
            }
            controller.close();
          },
        });
        return new Response(stream, {
          status: 200,
          headers: { "Content-Type": "text/event-stream" },
        });
      };
    });

    await page.goto(`${server.base}/#/s/${sid}/chat`);
    await expect(page.getByTestId("chat-section")).toBeVisible({ timeout: 15000 });

    await page.getByTestId("composer-input").fill("build the tamagotchi feature");
    await page.getByTestId("composer-send").click();

    const bubble = page.getByTestId("thinking-bubble");
    await expect(bubble).toBeVisible({ timeout: 10000 });

    // After the first thought only.
    await page.waitForTimeout(900);
    await shot(page, "bubble-first-thought");

    // After both tools have arrived: the thought must STAY above them.
    await page.waitForTimeout(1900);
    await shot(page, "bubble-tools-below-thought");

    // After the second thought + Read tool: full interleaved feed.
    await page.waitForTimeout(2500);
    await shot(page, "bubble-full-interleave");

    // Structural assertion: feed order inside the bubble is
    // thought, tool, tool, thought, tool — by reading the rendered rows.
    // (The rows are the shared ActivityFeed component — same classes the
    // preserved disclosure and the meta overlay render.)
    const rows = bubble.locator(".chat-activity__thought, .chat-activity__tool");
    await expect(rows).toHaveCount(5);
    const kinds: string[] = [];
    for (let i = 0; i < 5; i++) {
      const cls = (await rows.nth(i).getAttribute("class")) ?? "";
      kinds.push(cls.includes("chat-activity__thought") ? "think" : "tool");
    }
    expect(kinds).toEqual(["think", "tool", "tool", "think", "tool"]);

    // The brain glyph marks each thinking row.
    await expect(bubble.locator(".chat-activity__brain").first()).toHaveText("🧠");

    // Done frame lands → the live bubble dissolves into the final agent reply,
    // but the activity feed SURVIVES, collapsed inside the agent bubble.
    await expect(bubble).toBeHidden({ timeout: 15000 });
    const activity = page.getByTestId("chat-activity").last();
    await expect(activity).toBeVisible();
    await expect(activity.locator(".chat-activity__summary")).toHaveText(
      "🧠 2 thoughts · 3 tool calls",
    );
    // Collapsed by default — the feed body is hidden until the summary toggle.
    await expect(activity.locator(".chat-activity__feed")).toBeHidden();
    await shot(page, "after-done-collapsed");

    // Expanding shows the same interleaved feed the live bubble had.
    await activity.locator(".chat-activity__summary").click();
    await expect(activity.locator(".chat-activity__feed")).toBeVisible();
    const kept = activity.locator(".chat-activity__thought, .chat-activity__tool");
    await expect(kept).toHaveCount(5);
    await shot(page, "after-done-expanded");
  } finally {
    await page.close();
    await context.close();
    await browser.close();
    server.stop();
  }
});
