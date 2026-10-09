/**
 * MCP terminal demo — records an external coding agent (Claude Code, the POC)
 * driving the kitsoki studio MCP server end to end, by REPLAYING a committed
 * termcast cassette in xterm.js and filming it through the shared demo pipeline
 * (camera 1600×900 → ChapterRecorder → saveVideoAsMp4 25s floor → chapters.json).
 *
 * No-LLM by construction: the replay plays a static cast and never spawns `claude`
 * or `kitsoki mcp` (see casts/types.ts). Validate fast with WEB_CHAT_PACE=0
 * (assertions, throwaway `.fast.mp4`); record watch-speed with the default pace.
 */
import { test, expect } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";
import { cameraContext } from "./_helpers/camera.js";
import { makeCaption, captureDiagnostics, installCurtain, liftCurtain } from "./_helpers/demo.js";
import {
  PACE, dwell, prepareVideoDir, saveVideoAsMp4, writeChapters, makeShot, ChapterRecorder,
} from "./_helpers/recorder.js";
import { resolveCast } from "../casts/index.js";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const SPEC_REL = "tools/mcp-demo/tests/mcp-terminal.e2e.spec.ts";
const ARTIFACT_DIR = path.resolve(__dirname, "../../../.artifacts/mcp-demo");
const VIDEO_DIR = path.join(ARTIFACT_DIR, "_video");
const PORT = Number(process.env.MCP_DEMO_PORT ?? "4319");
const PLAYER_URL = `http://localhost:${PORT}/player/`;

test.describe.configure({ mode: "serial" });

test.beforeAll(() => prepareVideoDir(VIDEO_DIR));

test("records a kitsoki xterm terminal demo", async ({ browser }) => {
  test.setTimeout(180_000);
  const cast = resolveCast();
  const shot = makeShot(ARTIFACT_DIR);
  const chapters = new ChapterRecorder();

  const context = await browser.newContext(cameraContext({ recordVideoDir: VIDEO_DIR }));
  const page = await context.newPage();
  const video = page.video();
  const { mark, onThrow } = captureDiagnostics(page, ARTIFACT_DIR);

  try {
    // Title curtain hides the (trivial) goto/resize staging, then lifts on camera.
    await installCurtain(page, cast.curtainTitle ?? cast.title);
    await page.goto(PLAYER_URL);
    await page.waitForFunction(() => (window as unknown as { __ready?: boolean }).__ready === true);
    await page.evaluate(([title, cols, rows, footer, badge]) => {
      const w = window as unknown as {
        __title: (t: string) => void;
        __meta: (m: { title?: string; footer?: string; badge?: string }) => void;
        __resize: (c: number, r: number) => Promise<void>;
      };
      w.__meta({ title: title as string, footer: footer as string, badge: badge as string });
      w.__title(title as string);
      return w.__resize(cols as number, rows as number);
    }, [cast.title, cast.cols, cast.rows, cast.footer ?? "", cast.badge ?? "MCP"] as const);
    const caption = await makeCaption(page, 4000);
    await liftCurtain(page);

    for (const beat of cast.beats) {
      mark(beat.id);
      chapters.open(beat.id, beat.label, SPEC_REL);
      await caption(beat.caption, beat.sub ?? "", 300);

      for (const chunk of beat.chunks) {
        if (chunk.kind === "type") {
          // Operator typing: echo a prompt marker, then reveal char-by-char.
          await page.evaluate(() => (window as unknown as { __feed: (s: string) => Promise<void> }).__feed("\x1b[36m> \x1b[0m"));
          for (const ch of [...chunk.data]) {
            await page.evaluate((c) => (window as unknown as { __feed: (s: string) => Promise<void> }).__feed(c), ch);
            await dwell(page, 22);
          }
        } else {
          await page.evaluate((d) => (window as unknown as { __feed: (s: string) => Promise<void> }).__feed(d), chunk.data);
          await dwell(page, 260);
        }
      }

      await page.evaluate(() => document.getElementById("demo-caption")?.classList.remove("show"));
      await dwell(page, 550);
      await shot(page, beat.id);
      await dwell(page, beat.holdMs ?? 4000);
      chapters.close();
    }

    // Assertions that hold at ANY pace (so WEB_CHAT_PACE=0 is a real gate): the
    // terminal painted the agent's closing line and the rendered TUI room.
    const text = await page.evaluate(() => document.querySelector(".xterm")?.textContent ?? "");
    expect(cast.beats.length).toBeGreaterThanOrEqual(6);
    for (const expected of cast.assertText ?? ["kitsoki"]) {
      expect(text.toLowerCase()).toContain(expected.toLowerCase());
    }
  } catch (err) {
    onThrow(err);
    await shot(page, "ERROR");
    throw err;
  } finally {
    await context.close(); // finalises the video
    const mp4 = await saveVideoAsMp4(video, ARTIFACT_DIR, cast.agent);
    writeChapters(mp4, chapters.list());
    await page.close().catch(() => {});
  }
});
