/**
 * Unit-style coverage for the "stale kitsoki process still holding the port"
 * self-healing added to `startWebServer` (tools/runstatus/tests/playwright/
 * _helpers/server.ts). NOT a demo/recording spec — no camera, no chapters, no
 * artifacts — just direct exercise of the helper against a fixed scratch port
 * that no other spec uses (see the port grep sweep in review; 7788 is free).
 *
 * Background: multiple worktrees running Playwright specs that spawn
 * `kitsoki web` on a fixed port occasionally left a previous run's server
 * process alive (Playwright's kill-on-failure doesn't always reap the full
 * `go run` -> compiled-binary process tree). The NEXT run against the same
 * port then failed with a misleading "server not healthy" or "address already
 * in use" error that read like a bug in the feature under test. This spec
 * proves the fix: detect + reap a stale KITSOKI occupant, but never touch an
 * unrelated process.
 */
import { test, expect } from "@playwright/test";
import { spawn, type ChildProcess } from "child_process";
import net from "net";
import fs from "fs";
import os from "os";
import path from "path";
import { startWebServer, repoRoot, STORIES_DIR } from "./_helpers/server.js";

const PORT = 7788;
const ADDR = `127.0.0.1:${PORT}`;
const FLOW = path.join(repoRoot, "stories", "prd", "flows", "happy_path.yaml");

function killGroup(proc: ChildProcess): void {
  if (proc.pid) {
    try {
      process.kill(-proc.pid, "SIGKILL");
    } catch {
      try {
        proc.kill("SIGKILL");
      } catch {
        // already gone
      }
    }
  }
}

test.describe("startWebServer stale-port self-healing", () => {
  test.beforeAll(() => {
    for (const p of [STORIES_DIR, FLOW]) {
      if (!fs.existsSync(p)) throw new Error(`missing required path: ${p}`);
    }
  });

  test("(c) a genuinely free port starts normally with no stale-process noise", async () => {
    const server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORIES_DIR });
    try {
      expect(server.base).toBe(`http://${ADDR}`);
      const res = await fetch(`${server.base}/`);
      expect(res.status).toBe(200);
    } finally {
      server.stop();
    }
  });

  test("(a)+(b) a leftover `kitsoki web` on the port is detected, killed, and the new server starts healthy", async () => {
    // Simulate a stale prior run: a real `go run ./cmd/kitsoki web` left
    // holding PORT, with nothing left tracking/stopping it (the exact shape
    // of the bug — an orphaned process tree).
    const tmpDbDir = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-stale-test-"));
    const stale = spawn(
      "go",
      ["run", "./cmd/kitsoki", "web", "--stories-dir", STORIES_DIR, "--addr", ADDR,
        "--db", path.join(tmpDbDir, "s.db"), "--flow", FLOW],
      { cwd: repoRoot, stdio: "ignore", detached: true },
    );
    stale.unref();

    // Wait until the stale server is actually up and holding the port, so the
    // scenario is real (not a race against a not-yet-listening process).
    const deadline = Date.now() + 30000;
    let up = false;
    while (Date.now() < deadline) {
      try {
        const res = await fetch(`http://${ADDR}/`);
        if (res.status === 200) {
          up = true;
          break;
        }
      } catch {
        // not up yet
      }
      await new Promise((r) => setTimeout(r, 200));
    }
    expect(up).toBe(true);

    let warned = "";
    const origWarn = console.warn;
    console.warn = (...args: unknown[]) => {
      warned += String(args[0]) + "\n";
      origWarn(...(args as []));
    };
    let server;
    try {
      // startWebServer must detect the occupant, recognize it as kitsoki-ish,
      // kill it, and then spawn+heal its OWN server on the same port.
      server = await startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORIES_DIR });
    } finally {
      console.warn = origWarn;
    }
    try {
      expect(warned).toMatch(/killed stale kitsoki process \d+ holding port 7788/);
      const res = await fetch(`${server.base}/`);
      expect(res.status).toBe(200);
    } finally {
      server.stop();
      // Belt-and-suspenders: make sure the simulated stale process is truly
      // gone (it should already be dead — this only cleans up if the test
      // itself failed before startWebServer's reap logic ran).
      killGroup(stale);
      fs.rmSync(tmpDbDir, { recursive: true, force: true });
    }
  });

  test("(d) a non-kitsoki occupant on the port is left alone and produces a clear error", async () => {
    const blocker = net.createServer((socket) => socket.end());
    await new Promise<void>((resolve, reject) => {
      blocker.once("error", reject);
      blocker.listen(PORT, "127.0.0.1", resolve);
    });
    try {
      await expect(
        startWebServer({ addr: ADDR, flow: FLOW, storiesDir: STORIES_DIR }),
      ).rejects.toThrow(/does not look like a kitsoki process/);
      // Prove it was left running (not killed).
      const stillUp = await new Promise<boolean>((resolve) => {
        const probe = net.createConnection({ host: "127.0.0.1", port: PORT }, () => {
          probe.end();
          resolve(true);
        });
        probe.once("error", () => resolve(false));
      });
      expect(stillUp).toBe(true);
    } finally {
      blocker.close();
    }
  });
});
