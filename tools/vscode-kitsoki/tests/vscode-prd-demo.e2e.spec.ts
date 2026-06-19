/**
 * vscode-prd-demo.e2e.spec.ts — the PRD editor demo: the brief/PRD mirrored into
 * a real VS Code editor, and a refine shown as a NATIVE DIFF with an in-editor
 * accept/reject verdict + inline comment. Deterministic, no-LLM.
 *
 * Two modes (mirrors vscode-tour.e2e.spec.ts):
 *   KITSOKI_VSCODE_PACE=0 (default) → fast/assert: every beat is a hard assertion
 *     in real VS Code (the in-editor visual gate). No dwells, no video.
 *   KITSOKI_VSCODE_PACE≥1 → paced/record: the same beats + dwells + recordVideo →
 *     .artifacts/vscode-prd-demo/vscode-prd-demo.mp4 + labeled NN-*.png (QA input).
 *
 * Determinism: the backend runs `kitsoki web --flow stories/prd/flows/
 * prd_editor_demo.yaml`. host.ide.* + host.artifacts_dir are UNSTUBBED, so the
 * extension's IDE server (connected via CLAUDE_CODE_SSE_PORT) opens REAL files and
 * shows a REAL diff. Free text routes through the deterministic verb-matcher /
 * default_intent (no LLM), exactly as in the native PRD web tour.
 */
import { test, expect, type FrameLocator, type Page } from '@playwright/test';
import * as fs from 'node:fs';
import * as path from 'node:path';
import * as os from 'node:os';
import { launchVSCode, packageExtension, type LaunchedVSCode } from './_helpers/launch';

const EXT_ROOT = path.resolve(__dirname, '..');
const REPO_ROOT = path.resolve(EXT_ROOT, '..', '..');
const STORY_DIR = path.join(REPO_ROOT, 'stories', 'prd');
const FLOW = path.join(STORY_DIR, 'flows', 'prd_editor_demo.yaml');

const PACE = Number.parseInt(process.env.KITSOKI_VSCODE_PACE ?? '0', 10) || 0;
const RECORD = PACE >= 1;
const GATE_DIR = path.join(REPO_ROOT, '.artifacts', 'vscode-prd-demo-gate');
const TOUR_DIR = path.join(REPO_ROOT, '.artifacts', 'vscode-prd-demo');
const ARTIFACT_DIR = RECORD ? TOUR_DIR : GATE_DIR;

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));
const dwell = (ms: number) => (RECORD ? sleep(ms * PACE) : Promise.resolve());

/** Find the webview guest frame that holds [data-testid=testid] (scans all hosts). */
async function surfaceFrame(win: Page, testid: string, timeoutMs = 30_000): Promise<FrameLocator> {
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

// WIP — fixme until the discovery driving is finished. The bridge it records is
// already proven by tests/ide-bridge.e2e.test.ts (pnpm test:bridge). What works
// here: launch + Open Chat + story picker + pop-out + the discovery pitch + the
// editor frame resolves. BLOCKER: the STANDALONE prd `idle` room is composer-only
// (no `start` intent button) and free-text routes to its default_intent (discuss)
// in flow mode, so the UI can't advance idle→search. The imported gears `prd.idle`
// (which the native web tour gears-prd-design.spec.ts drives) DOES render the
// intent buttons. Finish by either (A) basing this on the gears-rust story +
// reusing that tour's proven core__ drive sequence (needs a gears demo flow that
// leaves host.ide/host.artifacts_dir unstubbed + the design-side editor wiring),
// or (B) surfacing `start` as a clickable intent in prd idle. Then add the
// refine→diff→Accept beats (the diff editor + comment + accept affordance).
test.fixme('vscode prd demo — brief/PRD in the editor, refine shows a verdict-gated diff', async () => {
  test.setTimeout(300_000);
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

    // ── Open Chat → pick the prd story → pop out to the full panel ────────────
    await win.waitForSelector('.monaco-workbench', { timeout: 60_000 });
    const icon = win.locator('.activitybar [aria-label*="Kitsoki" i]').first();
    await expect(icon).toBeVisible({ timeout: 30_000 });
    await icon.click();
    await expect(win.locator('.pane-header').filter({ hasText: /^\s*Chat\b/i }).first()).toBeVisible({
      timeout: 30_000,
    });
    await runPaletteCommand(win, ['>Kitsoki: Open Chat']);
    await drivePicker(win, 'prd');
    // Pop out to the full editor-area panel (the expanded InputBar with widgets).
    // The pop-out is the Chat pane's title-bar button (hidden from the palette).
    await clickViewTitleAction(win, 'Chat', 'Open Chat in Editor');
    await win.locator('.tab.active').filter({ hasText: /Kitsoki/i }).first().waitFor({ timeout: 30_000 }).catch(() => undefined);

    // back-stories is present ONLY in the full editor panel (never the collapsed
    // sidebar surface), so this resolves the EXPANDED chat frame where the
    // structured widgets (composer, intent buttons, the refine param form) render.
    const chat = await surfaceFrame(win, 'back-stories', 45_000);
    const state = () => chat.locator('[data-testid="current-state"]');
    // DOM-dispatch every click (the native tour pattern) so it fires through any
    // SPA overlay regardless of paint order.
    const domClick = (loc: ReturnType<FrameLocator['locator']>) =>
      loc.first().evaluate((el) => (el as HTMLElement).click());
    const typeAndSend = async (textVal: string) => {
      const input = chat.locator('[data-testid="composer-input"]').first();
      await expect(input).toBeVisible({ timeout: 15_000 });
      await input.fill(textVal);
      await sleep(250); // let v-model settle so the keydown handler sends
      await input.press('Enter');
    };
    // Proceed by a verb: click its intent button when the room renders one
    // (choice rooms), else type the verb into the composer (semantic rooms — the
    // deterministic verb-matcher catches it, no LLM).
    const proceed = async (verb: string) => {
      const btn = chat.locator(`[data-testid="intent-btn-${verb}"]`).first();
      if (await btn.isVisible({ timeout: 8000 }).catch(() => false)) {
        await domClick(btn);
        return;
      }
      await typeAndSend(verb);
    };
    const fillRefine = async (feedback: string) => {
      const form = chat.locator('form[data-intent="refine"]').first();
      await expect(form).toBeVisible({ timeout: 15_000 });
      await form.locator('input').first().fill(feedback);
      await dwell(200);
      await domClick(form.locator('button[type="submit"]'));
    };

    // ── Discovery → drafting (deterministic verb-matcher / default_intent) ────
    await expect(state(), 'session starts in idle').toHaveText('idle', { timeout: 30_000 });
    await shot('a-idle');
    await typeAndSend('I want a notes service'); // → discuss
    await dwell(800);
    await proceed('start'); // → search
    await expect(state()).toHaveText('search', { timeout: 30_000 });
    await proceed('confirm'); // → clarifying
    await expect(state()).toHaveText('clarifying', { timeout: 30_000 });
    await typeAndSend('platform engineers; the metric is notes-saved-per-session'); // → answer
    await dwell(600);
    await typeAndSend('submit'); // → brief
    await expect(state(), 'brief room — the brief opens + grows in the editor').toHaveText('brief', {
      timeout: 30_000,
    });
    await shot('b-brief');
    await proceed('confirm'); // → references
    await expect(state()).toHaveText('references', { timeout: 30_000 });
    await proceed('confirm'); // → drafting
    await expect(state(), 'drafting — the PRD is authored + opened in the editor').toHaveText('drafting', {
      timeout: 30_000,
    });

    // ── The PRD opened in a real editor tab ──────────────────────────────────
    await expect(
      win.locator('.tab').filter({ hasText: /004-prd\.md/i }).first(),
      'the PRD draft opened as an editor tab',
    ).toBeVisible({ timeout: 30_000 });
    await shot('c-draft-in-editor');

    // ── Refine → a NATIVE DIFF with the feedback as an inline comment ────────
    await fillRefine('add a non-goals section and require tenant isolation');
    await expect(
      win.locator('.monaco-diff-editor').first(),
      'refine opens a native side-by-side diff',
    ).toBeVisible({ timeout: 30_000 });
    await shot('d-refine-diff');

    // The inline comment thread carries the feedback.
    await expect(
      win.locator('.review-comment, .comment-body').filter({ hasText: /tenant isolation/i }).first(),
      'the refine feedback shows as an inline comment',
    ).toBeVisible({ timeout: 15_000 });

    // ── Accept the change IN the diff (native editor title action) ───────────
    await win
      .locator('.editor-actions .action-label[aria-label*="Accept" i], .codelens-decoration a:has-text("Accept")')
      .first()
      .click({ timeout: 15_000 });
    // The diff closes and the PRD (now v2) is the focused tab — verdict applied.
    await expect(
      win.locator('.monaco-diff-editor').first(),
      'accepting the diff closes the diff editor',
    ).toBeHidden({ timeout: 30_000 });
    await shot('e-accepted');

    // Back in the chat, the drafting view reflects v2 (Non-Goals).
    await expect(
      chat.locator('[data-testid="chat-transcript"]').getByText(/Non-Goals/i).first(),
      'the accepted refine promoted v2 into the chat view',
    ).toBeVisible({ timeout: 20_000 });
    await shot('f-v2-in-chat');
  } finally {
    if (launched) await launched.app.close().catch(() => undefined);
  }
});

// ── Native VS Code chrome helpers (copied from vscode-tour.e2e — proven) ─────
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
