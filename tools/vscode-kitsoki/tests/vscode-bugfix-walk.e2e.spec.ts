/**
 * vscode-bugfix-walk.e2e.spec.ts — the VS CODE proof of the bugfix pipeline
 * (WS-D D2): idle → …cascade… → implementing → …cascade… → reviewing →
 * …cascade… → done → @exit:done, driven inside a REAL VS Code window against
 * the SAME fixture the flow-test suite uses (`stories/bugfix/flows/
 * happy_llm.yaml` — `judge_mode: llm`, inline `host_handlers` so every
 * `host.agent.*` call returns the same canned artifact regardless of which
 * room made it or how many times), through the extension's `kitsoki.flow` /
 * `kitsoki.storiesDir` settings (the pattern established by
 * vscode-prd-demo.e2e.spec.ts and vscode-deliver-decompose-walk.e2e.spec.ts).
 *
 * happy_llm.yaml (inline `host_handlers`) is used here instead of
 * `happy_human.yaml` (a `host_cassette`) deliberately: `kitsoki web --flow`
 * (unlike `kitsoki test flows`) runs a freshly seeded initial state's
 * on_enter for real, and idle's on_enter (rooms/idle.yaml) makes its own
 * preflight `host.agent.task` call — one MORE call than the cassette was
 * recorded expecting (the cassette was captured under `kitsoki test flows`,
 * whose harness bypasses that on_enter entirely), so replay one-off
 * mismatches and bounces to idle via on_error. Inline `host_handlers` have no
 * such positional/episode sensitivity (same canned data for every call), so
 * `happy_llm.yaml` sidesteps the gap entirely — a real, load-bearing
 * difference between the two flow-fixture styles worth remembering.
 *
 * judge_mode=llm's auto-cascading judge (each checkpoint's on_enter binds an
 * artifact, fires the judge, and emit_intents `accept`) races the operator's
 * own click once on_enter runs for real (a manual `accept` and the
 * in-flight auto-cascade can both be advancing state at once), so this walk
 * does NOT assert a fixed intermediate stop after each click — only that the
 * state repeatedly progresses and eventually SETTLES (stops changing) at the
 * cascade's documented endpoint for that leg.
 *
 * Deterministic, no-LLM (`--flow` stubs every host.agent.* call). Fast by
 * default (no dwells/video) — set KITSOKI_VSCODE_PACE>=1 to record a paced
 * walk.
 *
 * SECOND JOB — the TODO(schema) capture (WS-D D2 deliverable 4): this walk's
 * `validating` room unconditionally invokes `host.ide.get_diagnostics` on
 * entry (rooms/validating.yaml), and because the extension's own IDE-MCP
 * server is always up (CLAUDE_CODE_SSE_PORT points the spawned backend back
 * at THIS window — see backend.ts), that call is NOT stubbed by the flow
 * fixture — it round-trips over the real MCP-over-ws wire to the real
 * `IdeTools.getDiagnostics` (ide-tools.ts), exactly like a live editor would
 * see it. This is the "one real-socket round-trip" ide-integration.md follow-up
 * 1 asked for. The extension now logs the received args (see ide-tools.ts);
 * this spec greps the extension-host log the harness already captures
 * (KITSOKI_E2E_LOG) and asserts the CONFIRMED wire shape: `path` (not `uri` —
 * a real mismatch this capture caught; see internal/host/ide_handlers.go's
 * IDEGetDiagnosticsHandler doc comment for the fix).
 */
import { test, expect, type FrameLocator, type Page } from '@playwright/test';
import * as fs from 'node:fs';
import * as path from 'node:path';
import * as os from 'node:os';
import { launchVSCode, packageExtension, type LaunchedVSCode } from './_helpers/launch';

const EXT_ROOT = path.resolve(__dirname, '..');
const REPO_ROOT = path.resolve(EXT_ROOT, '..', '..');
const STORY_DIR = path.join(REPO_ROOT, 'stories');
const FLOW = path.join(REPO_ROOT, 'stories', 'bugfix', 'flows', 'happy_llm.yaml');

const PACE = Number.parseInt(process.env.KITSOKI_VSCODE_PACE ?? '0', 10) || 0;
const RECORD = PACE >= 1;
const ARTIFACT_DIR = path.join(REPO_ROOT, '.artifacts', RECORD ? 'vscode-bugfix-walk' : 'vscode-bugfix-walk-gate');

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));
const dwell = (ms: number) => (RECORD ? sleep(ms * PACE) : Promise.resolve());

/** Resolve the chat webview's innermost frame (mirrors the other vscode e2e specs). */
async function surfaceFrame(win: Page, testid: string, timeoutMs = 45_000): Promise<FrameLocator> {
  const inners = ['iframe[title]', 'iframe[name="active-frame"]', 'iframe'];
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const count = await win.locator('iframe.webview').count().catch(() => 0);
    for (let i = 0; i < count; i++) {
      for (const inner of inners) {
        const fl = win.frameLocator('iframe.webview').nth(i).frameLocator(inner).first();
        try {
          await fl.locator(`[data-testid="${testid}"]`).first().waitFor({ timeout: 1000 });
          return fl;
        } catch {
          /* next */
        }
      }
    }
    await sleep(250);
  }
  throw new Error(`no webview frame with [data-testid="${testid}"] in ${timeoutMs}ms`);
}

async function openQuickInput(win: Page, openKeys: string) {
  const input = win.getByRole('combobox', { name: 'input' });
  for (let attempt = 0; attempt < 5; attempt++) {
    await win.keyboard.press(openKeys);
    const opened = await input.waitFor({ timeout: 2000 }).then(() => true).catch(() => false);
    if (opened) return input;
    await win.keyboard.press('Escape').catch(() => undefined);
    await sleep(250);
  }
  return null;
}

async function runPaletteCommand(win: Page, queries: string[]): Promise<boolean> {
  const isMac = process.platform === 'darwin';
  const palette = isMac ? 'Meta+Shift+P' : 'Control+Shift+P';
  for (const query of queries) {
    const input = await openQuickInput(win, palette);
    if (!input) continue;
    await input.fill(query);
    await sleep(800);
    const hasMatch = await win
      .locator('.quick-input-list .monaco-list-row')
      .first()
      .isVisible({ timeout: 1500 })
      .catch(() => false);
    if (hasMatch) {
      await win.keyboard.press('Enter');
      await sleep(1200);
      return true;
    }
    await win.keyboard.press('Escape');
    await sleep(300);
  }
  return false;
}

async function drivePicker(win: Page, query: string): Promise<boolean> {
  const input = win.getByRole('combobox', { name: 'input' });
  await input.waitFor({ timeout: 8000 }).catch(() => undefined);
  await input.fill(query);
  await sleep(800);
  const hasMatch = await win
    .locator('.quick-input-list .monaco-list-row')
    .first()
    .isVisible({ timeout: 2000 })
    .catch(() => false);
  if (!hasMatch) {
    await win.keyboard.press('Escape');
    return false;
  }
  await win.keyboard.press('Enter');
  await sleep(1200);
  return true;
}

function paneByTitle(win: Page, title: string) {
  return win
    .locator('.pane')
    .filter({ has: win.locator('.pane-header').filter({ hasText: new RegExp(`^\\s*${title}\\b`, 'i') }) })
    .first();
}


test('vscode: bugfix happy_llm walk — auto-cascading judge settles at each leg, ending __exit__done, real IDE-link get_diagnostics captured', async () => {
  test.setTimeout(300_000);
  if (!fs.existsSync(FLOW)) throw new Error(`missing flow fixture: ${FLOW}`);

  const tmpRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'vscode-bugfix-walk-'));
  const workspace = path.join(tmpRoot, 'workspace');
  fs.mkdirSync(path.join(workspace, '.vscode'), { recursive: true });
  fs.writeFileSync(
    path.join(workspace, '.vscode', 'settings.json'),
    JSON.stringify(
      {
        'kitsoki.flow': FLOW,
        'kitsoki.storiesDir': STORY_DIR,
        'kitsoki.binaryPath': fs.existsSync(path.join(REPO_ROOT, 'bin', 'kitsoki'))
          ? path.join(REPO_ROOT, 'bin', 'kitsoki')
          : '',
        'git.enabled': false,
        'git.openRepositoryInParentFolders': 'never',
        'editor.minimap.enabled': false,
        'workbench.tips.enabled': false,
        'workbench.startupEditor': 'none',
      },
      null,
      2,
    ),
  );

  const extensionsDir = packageExtension(EXT_ROOT, path.join(tmpRoot, 'extensions'));
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  const logPath = path.join(ARTIFACT_DIR, 'extension-host.log');
  process.env.KITSOKI_E2E_LOG = logPath;
  fs.writeFileSync(logPath, '');

  let shotIdx = 0;
  let launched: LaunchedVSCode | undefined;

  try {
    launched = await launchVSCode({
      workspace,
      extensionsDir,
      userDataDir: path.join(tmpRoot, 'user-data'),
      size: { width: 1400, height: 900 },
      ...(RECORD ? { videoDir: path.join(ARTIFACT_DIR, 'video') } : {}),
    });
    const { win } = launched;

    const shot = async (label: string) => {
      const n = String(++shotIdx).padStart(2, '0');
      await win.screenshot({ path: path.join(ARTIFACT_DIR, `${n}-${label}.png`) }).catch(() => undefined);
    };

    // ── Open Chat → pick the bugfix story → maximize the bottom Chat panel ────
    await win.waitForSelector('.monaco-workbench', { timeout: 60_000 });
    const opened = await runPaletteCommand(win, ['>Kitsoki: Open Chat']);
    if (!opened) throw new Error('could not run "Kitsoki: Open Chat" from the command palette');
    // Story title is "Bug-fix pipeline" (stories/bugfix/app.yaml) — match the
    // literal hyphenated substring so VS Code's quick-pick fuzzy scorer hits.
    const picked = await drivePicker(win, 'Bug-fix pipeline');
    if (!picked) throw new Error('could not pick the "Bug-fix pipeline" story from the quick pick');
    await runPaletteCommand(win, ['>View: Toggle Maximized Panel']);
    await sleep(600);

    const chat = await surfaceFrame(win, 'surface-chat', 45_000);
    const state = () => chat.locator('[data-testid="current-state"]');

    /** Poll current-state until it stops changing for `quietMs`, or give up at `maxMs`. */
    const settle = async (quietMs = 1500, maxMs = 45_000): Promise<string> => {
      const deadline = Date.now() + maxMs;
      let last = (await state().textContent().catch(() => '')) ?? '';
      let lastChange = Date.now();
      while (Date.now() < deadline) {
        await sleep(400);
        const cur = (await state().textContent().catch(() => '')) ?? '';
        if (cur !== last) {
          last = cur;
          lastChange = Date.now();
        } else if (Date.now() - lastChange >= quietMs) {
          return last;
        }
      }
      return last;
    };

    // Focus + Enter, NOT a coordinate click: this story's action grid has a
    // (pre-existing, out of scope here) CSS stacking issue where a later
    // row's button visually intercepts pointer events over an earlier row's
    // button at the SAME screen coordinates — a real click (even
    // Playwright's `force:true`, which only skips actionability checks, not
    // browser hit-testing) lands on whichever button the browser resolves
    // at that point, not necessarily this locator's own element. Focusing
    // the exact DOM node and pressing Enter fires its native button
    // activation regardless of what's visually on top.
    const clickIntent = async (intent: string) => {
      const matches = chat.locator(`[data-testid="intent-btn-${intent}"]`);
      const n = await matches.count();
      if (n !== 1) {
        const texts = await matches.allTextContents().catch(() => []);
        throw new Error(`intent-btn-${intent}: expected exactly 1 match, got ${n}: ${JSON.stringify(texts)}`);
      }
      const btn = matches.first();
      await expect(btn).toBeVisible({ timeout: 20_000 });
      await dwell(700);
      await btn.focus();
      await win.keyboard.press('Enter');
      await dwell(600);
    };

    // idle's on_enter auto-emits `start` unconditionally when a ticket is
    // present (rooms/idle.yaml) — under `kitsoki web --flow` (unlike
    // `kitsoki test flows`) that on_enter runs for real on session creation,
    // so an idle → reproducing → …cascade… chain plays out with no button
    // click at all. Each checkpoint's own on_enter (bind artifact → fire
    // judge → emit_intent accept) then races a manual `accept`, so rather
    // than asserting a fixed intermediate stop, drive `accept` repeatedly
    // whenever the state SETTLES short of the terminal exit — the judge
    // cascade and this loop converge on the same documented endpoint
    // (happy_llm.yaml's `__exit__done`) either way. validating's on_enter
    // pulls host.ide.get_diagnostics for real (see this file's header
    // comment) somewhere in this cascade.
    const seenStates: string[] = [];
    let cur = await settle();
    seenStates.push(cur);
    await shot(`a-${cur}`);
    for (let i = 0; i < 10 && cur !== '__exit__done'; i++) {
      await clickIntent('accept');
      cur = await settle();
      seenStates.push(cur);
      await shot(`b${i}-${cur}`);
    }
    expect(cur, `states visited: ${seenStates.join(' -> ')}`).toBe('__exit__done');
  } finally {
    if (launched) await launched.app.close().catch(() => undefined);
  }

  // ── TODO(schema) capture assertion ──────────────────────────────────────
  // The extension-host log tees every OutputChannel line (teeToFile in
  // extension.ts); ide-tools.ts now logs the getDiagnostics args it receives.
  // Assert the CONFIRMED wire shape: `path`, not the old best-effort `uri`.
  const hostLog = fs.readFileSync(logPath, 'utf8');
  const diagLine = hostLog.split('\n').find((l) => l.includes('[ide] getDiagnostics args='));
  expect(diagLine, `extension-host log should contain a getDiagnostics call:\n${hostLog.slice(-4000)}`).toBeTruthy();
  expect(diagLine).toContain('"path"');
  expect(diagLine).not.toContain('"uri"');
});
