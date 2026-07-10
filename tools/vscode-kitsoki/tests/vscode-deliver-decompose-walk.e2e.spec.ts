/**
 * vscode-deliver-decompose-walk.e2e.spec.ts — the VS CODE proof of the
 * decompose-vs-direct chain (proposal
 * docs/proposals/deliver-canonical-decomposition.md, task B4 5.2): dev-story's
 * `design_done` room hands off to `deliver` via `go_deliver`, which decomposes
 * the published proposal into briefs, lints/reviews them, and fans `fleet`
 * over the result — driven inside a REAL VS Code window against the SAME
 * fixture the web proof uses (`stories/dev-story/flows/
 * design_to_decompose_to_impl.yaml`), through the extension's `kitsoki.flow` /
 * `kitsoki.storiesDir` settings (the pattern established by
 * vscode-prd-demo.e2e.spec.ts).
 *
 * First-class, not a copy of the web spec: it drives the SAME conversation
 * through VS Code's own chrome (`kitsoki.flow` workspace setting → the
 * extension spawns `kitsoki web` as its backend → the chat renders inside a
 * VS Code webview iframe, resolved via `surfaceFrame` — the extension's own
 * iframe-descent helper, not Playwright's raw page).
 *
 * Requires `kitsoki.mode: "one-shot"` (new: B4 5.2 adds the `kitsoki.mode`
 * setting, mirroring `kitsoki web --mode`) — deliver's decompose → lint →
 * review chain auto-advances through synthetic emit/decision gates with no
 * operator-facing button at each hop; the extension's default `staged` posture
 * (matches `kitsoki web`'s own default) would stall at the first gate exactly
 * as it does in the web spec without the override.
 *
 * Deterministic, no-LLM (`--flow` stubs every host.* call — decomposer/
 * reviewer agent responses, fleet's integrate/verify/cleanup execs — and
 * `--mode one-shot` auto-advances through the gates). Fast by default (no
 * dwells/video) — set KITSOKI_VSCODE_PACE>=1 to record a paced walk.
 */
import { test, expect, type FrameLocator, type Page } from '@playwright/test';
import * as fs from 'node:fs';
import * as path from 'node:path';
import * as os from 'node:os';
import { launchVSCode, packageExtension, type LaunchedVSCode } from './_helpers/launch';

const EXT_ROOT = path.resolve(__dirname, '..');
const REPO_ROOT = path.resolve(EXT_ROOT, '..', '..');
const STORY_DIR = path.join(REPO_ROOT, 'stories');
const FLOW = path.join(REPO_ROOT, 'stories', 'dev-story', 'flows', 'design_to_decompose_to_impl.yaml');

const PACE = Number.parseInt(process.env.KITSOKI_VSCODE_PACE ?? '0', 10) || 0;
const RECORD = PACE >= 1;
const ARTIFACT_DIR = path.join(
  REPO_ROOT,
  '.artifacts',
  RECORD ? 'vscode-deliver-decompose-walk' : 'vscode-deliver-decompose-walk-gate',
);

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));
const dwell = (ms: number) => (RECORD ? sleep(ms * PACE) : Promise.resolve());

/** Resolve the chat webview's innermost frame (mirrors vscode-prd-demo.e2e.spec.ts). */
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

async function clickViewTitleAction(win: Page, paneTitle: string, actionLabel: string): Promise<void> {
  const pane = paneByTitle(win, paneTitle);
  await pane.locator('.pane-header').hover().catch(() => undefined);
  await pane
    .locator(`.actions-container a[aria-label*="${actionLabel}" i], .actions-container a[title*="${actionLabel}" i]`)
    .first()
    .click()
    .catch(() => undefined);
}

test('vscode: design_done → go_deliver → deliver decompose/lint/review → fleet fan-out → landing', async () => {
  test.setTimeout(300_000);
  if (!fs.existsSync(FLOW)) throw new Error(`missing flow fixture: ${FLOW}`);

  const tmpRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'vscode-deliver-decompose-'));
  const workspace = path.join(tmpRoot, 'workspace');
  fs.mkdirSync(path.join(workspace, '.vscode'), { recursive: true });
  fs.writeFileSync(
    path.join(workspace, '.vscode', 'settings.json'),
    JSON.stringify(
      {
        'kitsoki.flow': FLOW,
        'kitsoki.storiesDir': STORY_DIR,
        // one-shot: deliver's decompose → lint → review chain auto-advances
        // through synthetic emit/decision gates with no operator button at
        // each hop — the default staged posture would stall at deliver.lint,
        // exactly as it does under `kitsoki web` without the same override
        // (see deliver-decompose-walk.spec.ts).
        'kitsoki.mode': 'one-shot',
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
  process.env.KITSOKI_E2E_LOG = path.join(ARTIFACT_DIR, 'extension-host.log');
  fs.writeFileSync(process.env.KITSOKI_E2E_LOG, '');

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

    // ── Open Chat → pick the dev-story story → pop out to the full editor panel ─
    await win.waitForSelector('.monaco-workbench', { timeout: 60_000 });
    const icon = win.locator('.activitybar [aria-label*="Kitsoki" i]').first();
    await expect(icon).toBeVisible({ timeout: 30_000 });
    await icon.click();
    await expect(win.locator('.pane-header').filter({ hasText: /^\s*Chat\b/i }).first()).toBeVisible({
      timeout: 30_000,
    });
    await runPaletteCommand(win, ['>Kitsoki: Open Chat']);
    await drivePicker(win, 'dev-story');
    await clickViewTitleAction(win, 'Chat', 'Open Chat in Editor');
    await win.locator('.tab.active').filter({ hasText: /Kitsoki/i }).first().waitFor({ timeout: 30_000 }).catch(() => undefined);

    // Minimise the sidebar so the chat fills the frame (closing sidebar
    // webviews re-indexes iframes, so do it before resolving the frame).
    await runPaletteCommand(win, ['>View: Close Primary Side Bar', '>View: Toggle Primary Side Bar']);
    await sleep(600);

    // back-stories is present ONLY in the full editor panel → the expanded frame
    // (mirrors vscode-prd-demo.e2e.spec.ts — resolving on it, not current-state
    // directly, avoids matching a stale/narrower frame before the chat mounts).
    const chat = await surfaceFrame(win, 'back-stories', 45_000);
    const state = () => chat.locator('[data-testid="current-state"]');
    const wait = (s: string) => expect(state()).toHaveText(s, { timeout: 30_000 });

    const domClick = (loc: ReturnType<FrameLocator['locator']>) =>
      loc.first().evaluate((el) => (el as HTMLElement).click());

    // Fleet re-enters the SAME `deliver.fleet.ship.configure` leaf room once per
    // brief (dispatch → ship.configure → ship.tail.verify → dispatch → …), so a
    // click whose expected state repeats the room the fixture is ALREADY
    // sitting in cannot be confirmed by text equality alone — the assertion
    // would trivially pass on the STALE pre-click text before the click's own
    // turn (and fleet's async re-dispatch to the next brief) has even run,
    // letting the walk race ahead of the backend. Capture the pre-click text
    // and first require it to CHANGE AWAY (proving a real turn — possibly
    // through fleet's transient dispatch/tail states — actually happened)
    // before waiting for the expected state to settle.
    const driveButton = async (intent: string, expectState: string) => {
      const before = (await state().textContent().catch(() => '')) ?? '';
      const btn = chat.locator(`[data-testid="intent-btn-${intent}"]`).first();
      await expect(btn).toBeVisible({ timeout: 20_000 });
      await dwell(700);
      await domClick(btn);
      await expect(state()).not.toHaveText(before, { timeout: 20_000 });
      await wait(expectState);
      await dwell(600);
    };

    // The fixture's initial_state (design_done) + initial_world (design_file)
    // are seeded with no slot-bearing intent required, so the fresh session
    // lands directly here — nothing to type.
    await wait('design_done');
    await shot('a-design-done');

    // ── design_done → deliver.configure (decompose-vs-direct: decompose arc) ──
    await driveButton('go_deliver', 'deliver.configure');
    await shot('b-deliver-configure');

    // ── deliver.configure → deliver.fleet.load (decompose → lint → review) ────
    await driveButton('deliver__start', 'deliver.fleet.load');
    await shot('c-fleet-load');

    // ── deliver.fleet.load → deliver.fleet.ship.configure (fan-out begins) ────
    await driveButton('deliver__fleet__start', 'deliver.fleet.ship.configure');

    // ── First brief ships (merge lock held; one more brief queued) ────────────
    await driveButton('deliver__fleet__ship__integrate_existing', 'deliver.fleet.ship.configure');

    // ── Second brief ships → fleet summary → @exit:done → landing ─────────────
    await driveButton('deliver__fleet__ship__integrate_existing', 'landing');
    await shot('d-landing');
  } finally {
    if (launched) await launched.app.close().catch(() => undefined);
  }
});
