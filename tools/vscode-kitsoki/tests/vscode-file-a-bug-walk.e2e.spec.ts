/**
 * vscode-file-a-bug-walk.e2e.spec.ts — the VS CODE proof of the in-product
 * "Report a bug" flow (WS-D D2), driven inside a REAL VS Code window: Meta
 * launcher → "Report a bug" → the capture-and-review modal (rrweb session
 * replay + scrubbed HAR) → describe → "File bug" → a toast naming the filed
 * path. Modelled on tools/runstatus/tests/playwright/pet-report-bug-capture.spec.ts
 * (the same REPORT_BUG_TOUR_STEPS-adjacent testids: meta-button, meta-menu,
 * meta-report-bug, bug-modal, bug-modal-title, bug-modal-description,
 * bug-modal-submit, bug-report-toast, bug-toast-path) and on the extension's
 * own `kitsoki.flow` / `kitsoki.storiesDir` pattern (vscode-prd-demo.e2e.spec.ts,
 * vscode-deliver-decompose-walk.e2e.spec.ts, vscode-bugfix-walk.e2e.spec.ts).
 *
 * The session backdrop reuses the SAME `stories/bugfix/flows/happy_llm.yaml`
 * fixture the bugfix walk spec drives — this spec only needs AN active
 * session open in the popped-out chat panel; it does not drive the pipeline
 * itself, so no cascading-judge settle logic is needed here.
 *
 * Filing is LOCAL: no `kitsoki.ticketRepo`-equivalent is configured (the
 * extension has no such setting; `runstatus.bug.preview`/file default to the
 * local `issues/bugs/<id>.md` path under the workspace, never a real GitHub
 * issue), so this is free and needs no GH_TOKEN.
 *
 * Deterministic, no-LLM: filing a bug is a client-capture + local-write RPC,
 * not an agent call — `--flow` here only backs the backdrop session.
 */
import { test, expect, type FrameLocator, type Page } from '@playwright/test';
import * as fs from 'node:fs';
import * as path from 'node:path';
import * as os from 'node:os';
import { launchVSCode, packageExtension, type LaunchedVSCode } from './_helpers/launch';

const EXT_ROOT = path.resolve(__dirname, '..');
const REPO_ROOT = path.resolve(EXT_ROOT, '..', '..');
const SRC_STORY_DIR = path.join(REPO_ROOT, 'stories');

const ARTIFACT_DIR = path.join(REPO_ROOT, '.artifacts', 'vscode-file-a-bug-walk-gate');

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

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

/** Click a pane's title-bar action — `kitsoki.popOutChat` is hidden from the
 * command palette, so it can only be driven via this view/title icon. */
async function clickViewTitleAction(win: Page, paneTitle: string, actionLabel: string): Promise<void> {
  const pane = paneByTitle(win, paneTitle);
  await pane.locator('.pane-header').hover().catch(() => undefined);
  await pane
    .locator(`.actions-container a[aria-label*="${actionLabel}" i], .actions-container a[title*="${actionLabel}" i]`)
    .first()
    .click()
    .catch(() => undefined);
}

const DEMO_TITLE = 'VS Code file-a-bug walk: sanity-check title';
const DEMO_DESCRIPTION =
  'Filed from the vscode-file-a-bug-walk e2e spec — proves the Meta launcher → ' +
  'Report a bug → capture/review modal → File bug chain works inside a real ' +
  'VS Code window, driven through the extension webview.';

test('vscode: Meta → Report a bug → review capture → describe → File bug → toast names the filed path', async () => {
  test.setTimeout(180_000);

  const tmpRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'vscode-file-a-bug-'));

  // Local bug filing resolves its target root via the STORY's on-disk git
  // toplevel BEFORE falling back to the backend's cwd (resolveBugRoot in
  // internal/runstatus/server/bug_report.go) — pointing `kitsoki.storiesDir`
  // straight at this checkout's real `stories/` would file the demo bug into
  // THIS repo's real `issues/bugs/` (a real mistake this spec first made).
  // Copy the whole stories/ tree into a tmp root with NO `.git` ancestor
  // (mirrors tools/runstatus/tests/playwright/pet-report-bug-capture.spec.ts)
  // so gitToplevel comes up empty and the workspace tmp dir is used instead.
  const storyDir = path.join(tmpRoot, 'stories');
  fs.cpSync(SRC_STORY_DIR, storyDir, { recursive: true });
  const flow = path.join(storyDir, 'bugfix', 'flows', 'happy_llm.yaml');
  if (!fs.existsSync(flow)) throw new Error(`missing flow fixture: ${flow}`);

  const workspace = path.join(tmpRoot, 'workspace');
  fs.mkdirSync(path.join(workspace, '.vscode'), { recursive: true });
  fs.writeFileSync(
    path.join(workspace, '.vscode', 'settings.json'),
    JSON.stringify(
      {
        'kitsoki.flow': flow,
        'kitsoki.storiesDir': storyDir,
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
    });
    const { win } = launched;

    const shot = async (label: string) => {
      const n = String(++shotIdx).padStart(2, '0');
      await win.screenshot({ path: path.join(ARTIFACT_DIR, `${n}-${label}.png`) }).catch(() => undefined);
    };

    // ── Open Chat → pick the bugfix story → pop out to the full editor panel ──
    await win.waitForSelector('.monaco-workbench', { timeout: 60_000 });
    const icon = win.locator('.activitybar [aria-label*="Kitsoki" i]').first();
    await expect(icon).toBeVisible({ timeout: 30_000 });
    await icon.click();
    await expect(win.locator('.pane-header').filter({ hasText: /^\s*Chat\b/i }).first()).toBeVisible({
      timeout: 30_000,
    });
    const opened = await runPaletteCommand(win, ['>Kitsoki: Open Chat']);
    if (!opened) throw new Error('could not run "Kitsoki: Open Chat" from the command palette');
    const picked = await drivePicker(win, 'Bug-fix pipeline');
    if (!picked) throw new Error('could not pick the "Bug-fix pipeline" story from the quick pick');
    await clickViewTitleAction(win, 'Chat', 'Open Chat in Editor');
    await win.locator('.tab.active').filter({ hasText: /Kitsoki/i }).first().waitFor({ timeout: 30_000 }).catch(() => undefined);

    await runPaletteCommand(win, ['>View: Close Primary Side Bar', '>View: Toggle Primary Side Bar']);
    await sleep(600);

    const chat = await surfaceFrame(win, 'back-stories', 45_000);
    await shot('a-session-open');

    // Focus + Enter, NOT a coordinate click: the embedded webview's topbar
    // header sits over the meta-button/report-bug-item at their real screen
    // coordinates (a CSS stacking quirk in this narrow embed, out of scope
    // here — see the analogous note in vscode-bugfix-walk.e2e.spec.ts), so a
    // real click (even `force:true`, which only skips actionability checks,
    // not browser hit-testing) can land on the header instead of the button.
    // Focusing the exact DOM node and pressing Enter fires its native click
    // handler regardless of what's visually on top.
    const activate = async (locator: ReturnType<FrameLocator['locator']>) => {
      await locator.focus();
      await win.keyboard.press('Enter');
    };

    // ── Meta → Report a bug ──────────────────────────────────────────────────
    const metaButton = chat.locator('[data-testid="meta-button"]').first();
    await expect(metaButton).toBeVisible({ timeout: 20_000 });
    await activate(metaButton);
    const metaMenu = chat.locator('[data-testid="meta-menu"]').first();
    await expect(metaMenu).toBeVisible({ timeout: 10_000 });
    await shot('b-meta-menu');

    const reportBugItem = chat.locator('[data-testid="meta-report-bug"]').first();
    await expect(reportBugItem).toBeVisible({ timeout: 10_000 });
    await activate(reportBugItem);

    // ── The capture-and-review modal ─────────────────────────────────────────
    const modal = chat.locator('[data-testid="bug-modal"]').first();
    await expect(modal).toBeVisible({ timeout: 20_000 });
    await shot('c-bug-modal');

    // The rrweb replay renders asynchronously (it reconstructs the captured
    // DOM into an iframe) — wait for it, but non-fatally: the review content
    // isn't what this spec is proving, only that the capture→file chain works.
    const replay = chat.locator('[data-testid="bug-modal-replay"]').first();
    await expect(replay).toBeVisible({ timeout: 10_000 }).catch(() => undefined);

    const titleInput = chat.locator('[data-testid="bug-modal-title"]').first();
    await expect(titleInput).toBeVisible({ timeout: 10_000 });
    await titleInput.fill(DEMO_TITLE);

    const descInput = chat.locator('[data-testid="bug-modal-description"]').first();
    await expect(descInput).toBeVisible({ timeout: 10_000 });
    await descInput.fill(DEMO_DESCRIPTION);
    await shot('d-described');

    const submit = chat.locator('[data-testid="bug-modal-submit"]').first();
    await expect(submit).toBeVisible({ timeout: 10_000 });
    await activate(submit);

    // ── The filed-path toast ─────────────────────────────────────────────────
    const toast = chat.locator('[data-testid="bug-report-toast"]').first();
    await expect(toast).toBeVisible({ timeout: 20_000 });
    await shot('e-toast-shown');
    const toastPath = chat.locator('[data-testid="bug-toast-path"]').first();
    const toastError = chat.locator('[data-testid="bug-toast-error"]').first();
    await expect(toastPath.or(toastError)).toBeVisible({ timeout: 30_000 });
    await shot('f-toast-settled');
    if (await toastError.isVisible().catch(() => false)) {
      const errText = await toastError.textContent().catch(() => '');
      throw new Error(`bug report filing failed: ${errText}`);
    }
    const toastText = (await toastPath.textContent().catch(() => '')) ?? '';
    expect(toastText).toMatch(/Filed:.*issues[\\/]bugs[\\/]/);
    // Never the real checkout's own issues/bugs/ (the mistake this spec first
    // made before the tmp stories copy above — resolveBugRoot prefers the
    // story's on-disk dir over cwd).
    expect(toastText).not.toContain(REPO_ROOT);

    // Confirm a matching file actually landed somewhere inside the tmp root
    // (i.e. inside the tmp stories copy, not merely a UI-only claim) — walk
    // the tmp tree rather than assume the exact subdirectory resolveBugRoot
    // picked (it prefers the story's on-disk dir, which may itself resolve
    // through a git-toplevel probe on this machine's environment).
    const filedName = path.basename(toastText.replace(/^Filed:\s*/, '').trim());
    const findFile = (dir: string): boolean => {
      for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
        const p = path.join(dir, entry.name);
        if (entry.isDirectory()) {
          if (findFile(p)) return true;
        } else if (entry.name === filedName) {
          return true;
        }
      }
      return false;
    };
    expect(findFile(tmpRoot), `expected to find ${filedName} somewhere under ${tmpRoot}`).toBe(true);
  } finally {
    if (launched) await launched.app.close().catch(() => undefined);
    fs.rmSync(tmpRoot, { recursive: true, force: true });
  }
});
