import { test, expect } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import net from "node:net";
import path from "node:path";

const REPO_ROOT = path.resolve(import.meta.dirname, "..", "..", "..");

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

test("unmanaged process output cannot stamp bottom chrome into xterm scrollback", async ({ page }) => {
  const port = await freePort();
  const bridge = spawn(
    "go",
    [
      "run",
      "./cmd/kitsoki",
      "tui-serve",
      "--addr",
      `127.0.0.1:${port}`,
      "--exec",
      "go",
      "--",
      "run",
      "./internal/tui/testdata/terminal-output-probe",
    ],
    {
      cwd: REPO_ROOT,
      stdio: "ignore",
      detached: true,
    },
  );

  try {
    await page.goto(`/player/?ws=ws://127.0.0.1:${port}/pty`);
    await page.waitForFunction(() => (window as any).__ready === true);
    await expect
      .poll(() => page.evaluate(() => (window as any).__status()), { timeout: 60_000 })
      .toBe("connected");
    await expect
      .poll(() => page.evaluate(() => (window as any).__dumpBuffer()), { timeout: 60_000 })
      .toContain("PROBE-DONE");

    const buffer = await page.evaluate(() => (window as any).__dumpBuffer());
    expect(buffer.match(/operation: Fix bug \(gated\)/g) ?? []).toHaveLength(1);
    expect(buffer.match(/^Actions:$/gm) ?? []).toHaveLength(1);
    expect(buffer).toContain("content 1");
    expect(buffer).toContain("content 3");
  } finally {
    stopProcess(bridge);
  }
});
