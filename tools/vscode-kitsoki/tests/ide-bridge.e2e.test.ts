// ide-bridge.e2e.test.ts — the REAL end-to-end proof of the IDE bridge.
//
// Drives the actual `kitsoki web` backend against the actual extension IdeServer
// (with a recording tool-dispatcher standing in for the VS Code editor) through
// the real PRD walk, and asserts the full chain the stakeholder cares about:
//
//   - the backend discovers the extension's lock and connects its IDE link
//     (CLAUDE_CODE_SSE_PORT), so world.ide.connected is true in a web session;
//   - entering the drafting room dispatches host.ide.open_file for the PRD;
//   - a refine dispatches host.ide.open_diff with the staged proposal, BLOCKS
//     for the verdict, and on accept the story promotes the v2 artifact (the
//     editor applied the change).
//
// No VS Code, no Playwright — just the two real processes over the real MCP wire.
// The Playwright tour records the same walk for the video; this proves it WORKS.
//
// Gated (spawns the kitsoki binary): run with `pnpm test:bridge`.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as os from 'node:os';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { spawn, type ChildProcess } from 'node:child_process';
import { IdeServer, type ToolDispatcher } from '../src/ide-server';

const EXT_ROOT = path.resolve(__dirname, '..');
const REPO_ROOT = path.resolve(EXT_ROOT, '..', '..');
const KITSOKI_BIN = path.join(REPO_ROOT, 'kitsoki');
const PRD_APP = path.join(REPO_ROOT, 'stories', 'prd', 'app.yaml');
const DEMO_FLOW = path.join(REPO_ROOT, 'stories', 'prd', 'flows', 'prd_editor_demo.yaml');

const noopLog = { appendLine() {} };
const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

/** Records every tool call; auto-accepts diffs so the blocking turn unblocks. */
class RecordingTools implements ToolDispatcher {
  readonly calls: Array<{ name: string; args: Record<string, unknown> }> = [];
  verdict: 'accepted' | 'rejected' = 'accepted';
  async dispatch(name: string, args: Record<string, unknown>): Promise<unknown> {
    this.calls.push({ name, args });
    if (name === 'openDiff') return { ok: true, verdict: this.verdict };
    return { ok: true };
  }
  callsOf(name: string) {
    return this.calls.filter((c) => c.name === name);
  }
}

function allocatePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const net = require('node:net');
    const srv = net.createServer();
    srv.once('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const port = srv.address().port;
      srv.close(() => resolve(port));
    });
  });
}

async function rpc<T = any>(base: string, method: string, params: Record<string, unknown> = {}): Promise<T> {
  const res = await fetch(`${base}/rpc`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }),
  });
  const json = (await res.json()) as { result?: T; error?: { message: string } };
  if (json.error) throw new Error(`${method}: ${json.error.message}`);
  return json.result as T;
}

async function healthPoll(base: string, child: ChildProcess): Promise<void> {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) throw new Error(`backend exited (${child.exitCode}) before healthy`);
    try {
      const r = await fetch(`${base}/`, { method: 'GET' });
      if (r.ok) return;
    } catch {
      /* not up yet */
    }
    await sleep(200);
  }
  throw new Error('backend health poll timed out');
}

test('ide bridge: PRD walk opens the draft and a refine shows a verdict-gated diff', async (t) => {
  if (!fs.existsSync(KITSOKI_BIN)) {
    t.skip(`kitsoki binary not built at ${KITSOKI_BIN} (run: go build -o kitsoki ./cmd/kitsoki)`);
    return;
  }

  // The backend runs with cwd = a throwaway workspace; relative paths (the
  // author output_path) and host.artifacts_dir writes land under it.
  const workspace = fs.mkdtempSync(path.join(os.tmpdir(), 'kitsoki-bridge-'));
  const lockDir = path.join(workspace, '.claude', 'ide');

  // Stand up the extension's REAL IdeServer with a recording dispatcher.
  const tools = new RecordingTools();
  const ide = new IdeServer(noopLog, tools, { lockDir, workspaceFolders: [workspace] });
  const idePort = await ide.ready;
  assert.ok(idePort, 'ide server bound a port');

  // Spawn the REAL backend pointed at the demo flow, seeded to discover our lock.
  const port = await allocatePort();
  const base = `http://127.0.0.1:${port}`;
  const child = spawn(
    KITSOKI_BIN,
    ['web', '--addr', `127.0.0.1:${port}`, '--flow', DEMO_FLOW, '--stories-dir', path.dirname(PRD_APP)],
    {
      cwd: workspace,
      env: { ...process.env, CLAUDE_CODE_SSE_PORT: String(idePort), HOME: workspace },
      stdio: ['ignore', 'pipe', 'pipe'],
    },
  );
  let backendLog = '';
  child.stdout?.on('data', (d) => (backendLog += d.toString()));
  child.stderr?.on('data', (d) => (backendLog += d.toString()));

  t.after(async () => {
    child.kill();
    ide.dispose();
  });

  try {
    await healthPoll(base, child);

    const { session_id } = await rpc<{ session_id: string }>(base, 'runstatus.session.new', {
      story_path: PRD_APP,
    });
    assert.ok(session_id, 'session started');

    const submit = (intent: string, slots: Record<string, unknown> = {}) =>
      rpc(base, 'runstatus.session.submit', { session_id, intent, slots });

    // Walk discovery → drafting (the same turns the demo flow + recording drive).
    await submit('discuss', { message: 'I want a notes service' });
    await submit('start');
    await submit('confirm'); // search → clarifying
    await submit('answer', { text: 'platform engineers; metric is notes-saved-per-session' });
    await submit('submit_answers'); // → brief
    await submit('confirm'); // → references
    const draftingView = await rpc<any>(base, 'runstatus.session.view', { session_id }).catch(() => null);
    await submit('confirm'); // → drafting

    // Entering drafting must have opened the PRD in the "editor".
    await sleep(200);
    const openFiles = tools.callsOf('openFile');
    assert.ok(
      openFiles.some((c) => String(c.args.path).endsWith('004-prd.md')),
      `drafting should open the PRD draft; openFile calls: ${JSON.stringify(openFiles.map((c) => c.args.path))}`,
    );

    // Refine → a verdict-gated diff. With the IDE connected the story takes the
    // open_diff arc; our dispatcher auto-accepts, so the turn completes and the
    // staged v2 is promoted.
    tools.verdict = 'accepted';
    await submit('refine', { feedback: 'add a non-goals section and require tenant isolation' });

    const diffs = tools.callsOf('openDiff');
    assert.equal(diffs.length, 1, `refine should open exactly one diff; got ${diffs.length}`);
    const d = diffs[0].args;
    assert.ok(String(d.path).endsWith('004-prd.md'), `diff left = live PRD, got ${d.path}`);
    assert.ok(
      String(d.new_text_path).includes('004-prd-next'),
      `diff right = staged proposal, got ${d.new_text_path}`,
    );

    // The accepted verdict promoted v2 — the view now shows the Non-Goals section.
    const afterView = await rpc<any>(base, 'runstatus.session.view', { session_id });
    const viewText = JSON.stringify(afterView);
    assert.match(viewText, /Non-Goals/i, 'accepted refine promoted the v2 PRD into the view');
  } catch (e) {
    throw new Error(`${(e as Error).message}\n--- backend log ---\n${backendLog.slice(-3000)}`);
  }
});
