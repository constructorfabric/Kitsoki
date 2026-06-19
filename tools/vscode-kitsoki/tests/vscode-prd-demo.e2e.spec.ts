/**
 * vscode-prd-demo.e2e.spec.ts — the PRD editor demo: the brief/PRD mirrored into
 * a real VS Code editor, and a refine shown as a NATIVE DIFF with an in-editor
 * accept/reject verdict + inline comment. Deterministic, no-LLM.
 *
 * Drives the gears-rust PRD walk through the chat UI (the SAME story + proven
 * core__ drive sequence as the native web tour gears-prd-design.spec.ts), but
 * against a demo flow (stories/gears-rust/flows/prd_to_design_demo.yaml) that
 * leaves host.ide.* + host.artifacts_dir UNSTUBBED — so the extension's IDE
 * server (connected via CLAUDE_CODE_SSE_PORT) opens REAL files and shows a REAL
 * diff. The bridge mechanics are also proven headlessly by tests/ide-bridge.e2e.test.ts.
 *
 * Two modes (mirrors vscode-tour.e2e.spec.ts):
 *   KITSOKI_VSCODE_PACE=0 (default) → fast/assert: every beat is a hard assertion
 *     in real VS Code (the in-editor visual gate). No dwells, no video.
 *   KITSOKI_VSCODE_PACE≥1 → paced/record → .artifacts/vscode-prd-demo/*.mp4 + NN-*.png.
 */
import { test, expect, type FrameLocator, type Page } from '@playwright/test';
import * as fs from 'node:fs';
import * as path from 'node:path';
import * as os from 'node:os';
import { launchVSCode, packageExtension, saveRecordingAsMp4, type LaunchedVSCode } from './_helpers/launch';

const EXT_ROOT = path.resolve(__dirname, '..');
const REPO_ROOT = path.resolve(EXT_ROOT, '..', '..');
const STORY_DIR = path.join(REPO_ROOT, 'stories', 'gears-rust');
const FLOW = path.join(STORY_DIR, 'flows', 'prd_to_design_demo.yaml');

const PACE = Number.parseInt(process.env.KITSOKI_VSCODE_PACE ?? '0', 10) || 0;
const RECORD = PACE >= 1;
const GATE_DIR = path.join(REPO_ROOT, '.artifacts', 'vscode-prd-demo-gate');
const TOUR_DIR = path.join(REPO_ROOT, '.artifacts', 'vscode-prd-demo');
const ARTIFACT_DIR = RECORD ? TOUR_DIR : GATE_DIR;

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));
const dwell = (ms: number) => (RECORD ? sleep(ms * PACE) : Promise.resolve());

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

test('vscode prd demo — brief/PRD in the editor, refine shows a verdict-gated diff', async () => {
  test.setTimeout(360_000);
  if (!fs.existsSync(FLOW)) throw new Error(`missing demo flow: ${FLOW}`);

  const tmpRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'vscode-prd-demo-'));
  const workspace = path.join(tmpRoot, 'workspace');
  fs.mkdirSync(path.join(workspace, '.vscode'), { recursive: true });
  fs.writeFileSync(
    path.join(workspace, '.vscode', 'settings.json'),
    JSON.stringify(
      {
        'kitsoki.flow': FLOW,
        'kitsoki.storiesDir': STORY_DIR,
        'kitsoki.binaryPath': fs.existsSync(path.join(REPO_ROOT, 'kitsoki'))
          ? path.join(REPO_ROOT, 'kitsoki')
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

    // ── Open Chat → pick the gears story → pop out to the full editor panel ───
    await win.waitForSelector('.monaco-workbench', { timeout: 60_000 });
    const icon = win.locator('.activitybar [aria-label*="Kitsoki" i]').first();
    await expect(icon).toBeVisible({ timeout: 30_000 });
    await icon.click();
    await expect(win.locator('.pane-header').filter({ hasText: /^\s*Chat\b/i }).first()).toBeVisible({
      timeout: 30_000,
    });
    await runPaletteCommand(win, ['>Kitsoki: Open Chat']);
    await drivePicker(win, 'gears');
    await clickViewTitleAction(win, 'Chat', 'Open Chat in Editor');
    await win.locator('.tab.active').filter({ hasText: /Kitsoki/i }).first().waitFor({ timeout: 30_000 }).catch(() => undefined);

    // back-stories is present ONLY in the full editor panel → the expanded frame.
    const chat = await surfaceFrame(win, 'back-stories', 45_000);
    const state = () => chat.locator('[data-testid="current-state"]');
    const wait = (s: string) => expect(state()).toHaveText(s, { timeout: 30_000 });

    const domClick = (loc: ReturnType<FrameLocator['locator']>) =>
      loc.first().evaluate((el) => (el as HTMLElement).click());
    const typeAndSend = async (textVal: string) => {
      const input = chat.locator('[data-testid="composer-input"]').first();
      await expect(input).toBeVisible({ timeout: 15_000 });
      await input.fill(textVal);
      await sleep(250);
      await input.press('Enter');
    };
    const clickIntent = async (intent: string) => {
      const btn = chat.locator(`[data-testid="intent-btn-${intent}"]`).first();
      await expect(btn).toBeVisible({ timeout: 20_000 });
      await domClick(btn);
    };
    const fillRefine = async (feedback: string) => {
      const form = chat.locator('form[data-intent="core__prd__refine"]').first();
      await expect(form).toBeVisible({ timeout: 20_000 });
      await form.locator('input').first().fill(feedback);
      await sleep(250);
      await domClick(form.locator('button[type="submit"]'));
    };

    // ── Discovery → drafting (proven gears core__ drive; deterministic, no LLM) ─
    await wait('core.main');
    await shot('a-main');
    await typeAndSend('prd'); // → core.prd.idle
    await wait('core.prd.idle');
    await typeAndSend('I want a notes-service gear for the platform'); // discuss
    // Wait for the discovery reply to render — `start` distills the conversation,
    // so it must land before we advance (in fast mode dwell is a no-op).
    await expect(
      chat.locator('[data-testid="chat-transcript"]').getByText(/ties to the work/i).first(),
      'the discovery reply rendered before distilling the idea',
    ).toBeVisible({ timeout: 30_000 });
    await dwell(2500);
    await clickIntent('core__prd__start'); // → search
    await wait('core.prd.search');
    await dwell(1800);
    await clickIntent('core__prd__confirm'); // → clarifying
    await wait('core.prd.clarifying');
    await dwell(2000);
    await typeAndSend('platform engineers; the metric is notes-saved-per-session'); // answer
    await dwell(2000);
    await typeAndSend('submit'); // → brief (the brief opens + grows in the editor)
    await wait('core.prd.brief');
    await dwell(3500); // linger on the brief opening in the editor
    await shot('b-brief');
    await clickIntent('core__prd__confirm'); // → references
    await wait('core.prd.references');
    await dwell(1800);
    await clickIntent('core__prd__confirm'); // → drafting (the PRD is authored + opened)
    await wait('core.prd.drafting');

    // ── The PRD opened in a real editor tab ──────────────────────────────────
    await expect(
      win.locator('.tab').filter({ hasText: /004-prd\.md/i }).first(),
      'the PRD draft opened as a real editor tab',
    ).toBeVisible({ timeout: 30_000 });
    await dwell(5000); // linger on the full PRD in the editor (the headline beat)
    await shot('c-draft-in-editor');

    // ── Refine → a NATIVE DIFF with the feedback as an inline comment ────────
    await fillRefine('add a non-goals section and require tenant isolation');
    await expect(
      win.locator('.monaco-diff-editor').first(),
      'refine opens a native side-by-side diff',
    ).toBeVisible({ timeout: 30_000 });
    await expect(
      win.locator('.review-comment, .comment-body, .monaco-editor .comment-thread').filter({ hasText: /tenant isolation/i }).first(),
      'the refine feedback shows as an inline comment',
    ).toBeVisible({ timeout: 15_000 });
    await dwell(5500); // linger on the diff + the green Non-Goals/tenant-isolation additions + the comment
    await shot('d-refine-diff');

    // ── Accept the change IN the diff (native editor title action / codelens) ─
    await win
      .locator('.editor-actions .action-label[aria-label*="Accept" i], .codelens-decoration a:has-text("Accept")')
      .first()
      .click({ timeout: 15_000 });
    await expect(
      win.locator('.monaco-diff-editor').first(),
      'accepting closes the diff editor',
    ).toBeHidden({ timeout: 30_000 });
    await dwell(3500); // linger on the applied PRD
    await shot('e-accepted');

    // Back in the chat, the drafting view reflects v2 (Non-Goals).
    await expect(
      chat.locator('[data-testid="chat-transcript"]').getByText(/Non-Goals/i).first(),
      'the accepted refine promoted v2 into the chat view',
    ).toBeVisible({ timeout: 20_000 });
    await dwell(4000);
    await shot('f-v2-in-chat');
  } finally {
    if (launched) await launched.app.close().catch(() => undefined);
    // Transcode through the shared guard: the canonical vscode-prd-demo.mp4 is
    // written ONLY for a real (paced) recording of sufficient length; a fast run
    // or a too-short paced run gets a discriminated name and never the canonical.
    if (RECORD) {
      saveRecordingAsMp4({
        videoDir: path.join(ARTIFACT_DIR, 'video'),
        artifactDir: ARTIFACT_DIR,
        name: 'vscode-prd-demo',
        record: RECORD,
        crop: launched?.viewport,
      });
    }
  }
});

// ── Native VS Code chrome helpers (from vscode-tour.e2e — proven) ─────────────
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
