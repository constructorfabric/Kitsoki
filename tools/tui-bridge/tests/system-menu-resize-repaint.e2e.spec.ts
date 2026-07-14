import { expect, test, type Page } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";

const REPO_ROOT = path.resolve(import.meta.dirname, "..", "..", "..");
const RECORDING = path.join(REPO_ROOT, "tools", "tui-bridge", "fixtures", "dogfood-marathon-recording.yaml");
const HOST_CASSETTE = path.join(REPO_ROOT, "tools", "tui-bridge", "fixtures", "dogfood-marathon.host.cassette.yaml");

async function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      const port = typeof address === "object" && address ? address.port : 0;
      server.close(() => resolve(port));
    });
  });
}

function stopProcess(proc: ChildProcess): void {
  if (!proc.pid) return;
  try {
    process.kill(-proc.pid, "SIGKILL");
  } catch {
    proc.kill("SIGKILL");
  }
}

async function visibleRows(page: Page): Promise<number> {
  return page.evaluate(() => String((window as any).__dump()).split("\n").length);
}

async function resizeAndWait(page: Page, width: number, height: number, direction: "grow" | "shrink"): Promise<number> {
  const before = await visibleRows(page);
  await page.setViewportSize({ width, height });
  if (direction === "grow") {
    await expect.poll(() => visibleRows(page)).toBeGreaterThan(before);
  } else {
    await expect.poll(() => visibleRows(page)).toBeLessThan(before);
  }
  // The xterm fit happens synchronously; give the PTY one render turn to apply
  // the resize frame before issuing the next cycle.
  await page.waitForTimeout(150);
  await expect.poll(() => page.evaluate(() => (window as any).__status())).toBe("connected");
  return visibleRows(page);
}

type Viewport = { width: number; height: number };
type ResizeStep = Viewport & { direction: "grow" | "shrink" };

async function exerciseSystemMenuResize(page: Page, initial: Viewport, steps: ResizeStep[]): Promise<void> {
  const port = await freePort();
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-menu-repaint-"));
  const bridge = spawn(
    "go",
    [
      "run",
      "./cmd/kitsoki",
      "tui-serve",
      "--addr",
      `127.0.0.1:${port}`,
      "--",
      "run",
      "stories/dev-story/app.yaml",
      "--harness",
      "replay",
      "--recording",
      RECORDING,
      "--host-cassette",
      HOST_CASSETTE,
      "--db",
      path.join(tmpDir, "session.db"),
    ],
    {
      cwd: REPO_ROOT,
      stdio: "ignore",
      detached: true,
      env: { ...process.env, KITSOKI_REPO: REPO_ROOT },
    },
  );

  try {
    await page.setViewportSize(initial);
    await page.goto(`/player/?ws=ws://127.0.0.1:${port}/pty`);
    await page.waitForFunction(() => (window as any).__ready === true);
    await expect
      .poll(() => page.evaluate(() => (window as any).__status()), { timeout: 60_000 })
      .toBe("connected");
    await expect
      .poll(() => page.evaluate(() => (window as any).__dump()), { timeout: 60_000 })
      .toContain("Tab chat");

    await page.click("#term");
    await page.evaluate(() => (window as any).__focusTerm());
    await page.keyboard.press("Tab");
    await expect.poll(() => page.evaluate(() => (window as any).__dump())).toContain("picker dismissed");
    await page.keyboard.press("Escape");
    await expect.poll(() => page.evaluate(() => {
      const screen = String((window as any).__dump());
      return screen.includes("menu (") || screen.includes("[13] World");
    })).toBe(true);

    for (const step of steps) {
      await resizeAndWait(page, step.width, step.height, step.direction);
    }

    const resizedBuffer = await page.evaluate(() => (window as any).__dumpBuffer());
    expect(resizedBuffer.match(/^menu \(↑\/↓ to move,/gm) ?? []).toHaveLength(1);
    expect(resizedBuffer.match(/^\s*[▸ ]\s*\[1\] Exit\b/gm) ?? []).toHaveLength(1);

    for (let i = 0; i < 20; i += 1) {
      await page.keyboard.press("ArrowDown");
    }
    await expect.poll(() => page.evaluate(() => (window as any).__dump())).toContain("[13] World");

    const finalBuffer = await page.evaluate(() => (window as any).__dumpBuffer());
    expect(finalBuffer.match(/^menu \(↑\/↓ to move,/gm) ?? []).toHaveLength(1);
    for (let i = 1; i <= 13; i += 1) {
      expect((finalBuffer.match(new RegExp(`^\\s*[▸ ]\\s*\\[${i}\\] `, "gm")) ?? []).length).toBeLessThanOrEqual(1);
    }
  } finally {
    stopProcess(bridge);
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
}

test("system menu repaint stays out of scrollback across small-wide-small resize cycles", async ({ page }) => {
  const steps: ResizeStep[] = [];
  for (let i = 0; i < 4; i += 1) {
    steps.push(
      { width: 1200, height: 800, direction: "grow" },
      { width: 760, height: 430, direction: "shrink" },
    );
  }
  await exerciseSystemMenuResize(page, { width: 760, height: 430 }, steps);
});

test("system menu opened wide stays out of scrollback on its first shrink", async ({ page }) => {
  await exerciseSystemMenuResize(page, { width: 1200, height: 800 }, [
    { width: 760, height: 430, direction: "shrink" },
    { width: 1200, height: 800, direction: "grow" },
    { width: 760, height: 430, direction: "shrink" },
  ]);
});
