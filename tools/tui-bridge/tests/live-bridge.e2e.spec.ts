import { test, expect } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import net from "node:net";
import path from "node:path";

// End-to-end proof that a browser page can drive a REAL pty over a REAL
// websocket: real keystrokes in, real bytes out, no cassette/replay in the
// loop. The bridge spawns /bin/cat (deterministic, no LLM) per the
// playwright.config.ts webServer entry.
const BRIDGE_ADDR = process.env.TUI_BRIDGE_ADDR ?? "127.0.0.1:4700";
const REPO_ROOT = path.resolve(import.meta.dirname, "..", "..", "..");

async function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.once("error", reject);
    srv.listen(0, "127.0.0.1", () => {
      const addr = srv.address();
      const port = typeof addr === "object" && addr ? addr.port : 0;
      srv.close(() => resolve(port));
    });
  });
}

function startBridge(addr: string, command: string[] = ["--exec", "/bin/cat"]): ChildProcess {
  return spawn(
    "go",
    ["run", "./cmd/kitsoki", "tui-serve", "--addr", addr, ...command],
    { cwd: REPO_ROOT, stdio: ["ignore", "pipe", "pipe"], detached: true },
  );
}

function stopBridge(proc: ChildProcess): void {
  if (!proc.pid) return;
  try {
    process.kill(-proc.pid, "SIGKILL");
  } catch {
    proc.kill("SIGKILL");
  }
}

test("browser round-trips real keystrokes through the pty bridge", async ({ page }) => {
  await page.goto(`/player/?ws=ws://${BRIDGE_ADDR}/pty`);
  await page.waitForFunction(() => (window as any).__ready === true);
  await expect
    .poll(() => page.evaluate(() => (window as any).__status()))
    .toBe("connected");

  // Real keystrokes: xterm's paste/typing path, not a raw socket send from
  // the test — proves the page's own onData wiring works, not just the
  // bridge. xterm captures keystrokes via a hidden textarea that only
  // receives focus once the terminal element itself is clicked.
  await page.click("#term");
  await page.keyboard.type("hello-from-playwright");
  await page.keyboard.press("Enter");

  await expect
    .poll(() => page.evaluate(() => (window as any).__dump()), { timeout: 10_000 })
    .toContain("hello-from-playwright");
});

test("resize control frame reaches the pty before subsequent input", async ({ page }) => {
  await page.goto(`/player/?ws=ws://${BRIDGE_ADDR}/pty`);
  await page.waitForFunction(() => (window as any).__ready === true);
  await expect
    .poll(() => page.evaluate(() => (window as any).__status()))
    .toBe("connected");

  // /bin/cat doesn't report size, but the resize frame must not break the
  // connection or subsequent echo.
  await page.setViewportSize({ width: 900, height: 500 });
  await page.waitForTimeout(200); // let the resize listener fire and send
  await page.click("#term");
  await page.keyboard.type("still-alive-after-resize");
  await page.keyboard.press("Enter");

  await expect
    .poll(() => page.evaluate(() => (window as any).__dump()), { timeout: 10_000 })
    .toContain("still-alive-after-resize");
});

test("browser sends arrow-key escape sequences through the pty bridge", async ({ page }) => {
  test.setTimeout(90_000);
  const port = await freePort();
  const addr = `127.0.0.1:${port}`;
  const script =
    "bytes=$(dd bs=1 count=3 2>/dev/null | od -An -tx1 | tr -d ' \\n'); printf 'arrow:%s\\n' \"$bytes\"; sleep 30";
  const bridge = startBridge(addr, ["--exec", "/bin/sh", "--", "-lc", script]);

  try {
    await page.goto(`/player/?ws=ws://${addr}/pty`);
    await page.waitForFunction(() => (window as any).__ready === true);
    await expect
      .poll(() => page.evaluate(() => (window as any).__status()), { timeout: 60_000 })
      .toBe("connected");

    await page.click("#term");
    await page.keyboard.press("ArrowDown");
    await page.keyboard.press("Enter");

    await expect
      .poll(() => page.evaluate(() => (window as any).__dump()), { timeout: 10_000 })
      .toContain("arrow:1b5b42");
  } finally {
    stopBridge(bridge);
  }
});

test("player preserves ANSI color and bold attributes from the pty", async ({ page }) => {
  test.setTimeout(90_000);
  const port = await freePort();
  const addr = `127.0.0.1:${port}`;
  const script = "printf '\\033[1;31mBOLDRED\\033[0m plain \\033[38;2;20;200;120mTRUECOLOR\\033[0m\\n'; sleep 30";
  const bridge = startBridge(addr, ["--exec", "/bin/sh", "--", "-lc", script]);

  try {
    await page.goto(`/player/?ws=ws://${addr}/pty`);
    await page.waitForFunction(() => (window as any).__ready === true);
    await expect
      .poll(() => page.evaluate(() => (window as any).__status()), { timeout: 60_000 })
      .toBe("connected");
    await expect
      .poll(() => page.evaluate(() => (window as any).__dump()), { timeout: 10_000 })
      .toContain("TRUECOLOR");

    const boldRed = await page.evaluate(
      () => (window as any).__textAttrs("BOLDRED") as Array<{ bold: boolean; fgColorMode: number }>,
    );
    expect(boldRed).toHaveLength("BOLDRED".length);
    expect(boldRed.every((cell) => cell.bold)).toBe(true);
    expect(boldRed.some((cell) => cell.fgColorMode !== 0)).toBe(true);

    const trueColor = await page.evaluate(
      () => (window as any).__textAttrs("TRUECOLOR") as Array<{ bold: boolean; fgColorMode: number; fgColor: number }>,
    );
    expect(trueColor).toHaveLength("TRUECOLOR".length);
    expect(trueColor.every((cell) => !cell.bold)).toBe(true);
    expect(trueColor.some((cell) => cell.fgColorMode !== 0 && cell.fgColor !== 0)).toBe(true);
  } finally {
    stopBridge(bridge);
  }
});

test("player reconnects when bridge starts after the page", async ({ page }) => {
  test.setTimeout(90_000);
  const port = await freePort();
  const addr = `127.0.0.1:${port}`;

  await page.goto(`/player/?ws=ws://${addr}/pty`);
  await page.waitForFunction(() => (window as any).__ready === true);
  await expect
    .poll(() => page.evaluate(() => (window as any).__status()))
    .toMatch(/connecting|reconnecting/);

  const bridge = startBridge(addr);
  try {
    await expect
      .poll(() => page.evaluate(() => (window as any).__status()), { timeout: 60_000 })
      .toBe("connected");
    await page.click("#term");
    await page.keyboard.type("after-late-bridge");
    await page.keyboard.press("Enter");
    await expect
      .poll(() => page.evaluate(() => (window as any).__dump()), { timeout: 10_000 })
      .toContain("after-late-bridge");
  } finally {
    stopBridge(bridge);
  }
});

test("player exposes deterministic visible scroll helpers", async ({ page }) => {
  test.setTimeout(90_000);
  const port = await freePort();
  const addr = `127.0.0.1:${port}`;
  const script = "i=1; while [ $i -le 80 ]; do printf 'scroll-line-%02d\\n' \"$i\"; i=$((i+1)); done; sleep 30";
  const bridge = startBridge(addr, ["--exec", "/bin/sh", "--", "-lc", script]);

  try {
    await page.goto(`/player/?ws=ws://${addr}/pty`);
    await page.waitForFunction(() => (window as any).__ready === true);
    await expect
      .poll(() => page.evaluate(() => (window as any).__status()), { timeout: 60_000 })
      .toBe("connected");
    await expect
      .poll(() => page.evaluate(() => (window as any).__dump()), { timeout: 10_000 })
      .toContain("scroll-line-80");

    const top = await page.evaluate(() => (window as any).__scrollToTop());
    expect(top).toContain("scroll-line-01");

    const middle = await page.evaluate(() => (window as any).__scrollLines(20));
    expect(middle).toContain("scroll-line-21");

    const bottom = await page.evaluate(() => (window as any).__scrollToBottom());
    expect(bottom).toContain("scroll-line-80");
  } finally {
    stopBridge(bridge);
  }
});
