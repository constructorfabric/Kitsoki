/**
 * vscode-tour.e2e.spec.ts — THE deterministic, no-LLM end-to-end gate for the
 * Kitsoki VS Code extension, and the spine the demo-video recorder reuses.
 *
 * "One spec, two modes" (mirrors WEB_CHAT_PACE in tools/runstatus):
 *   KITSOKI_VSCODE_PACE=0  (default) → fast/assert: every critical-path beat is
 *                          a hard assertion, no dwells, no recordVideo, no
 *                          narration. This is the CI / de-risk gate. Same input
 *                          → same result.
 *   KITSOKI_VSCODE_PACE≥1  → paced/record: the SAME asserted beats plus per-beat
 *                          dwells, recordVideo (one ChapterRecorder clock), and the
 *                          editor-pane beats (open the story's app.yaml, open the
 *                          Kitsoki Trace panel). The recorder only ADDS on top of
 *                          the EXACT path this gate proves — it cannot drift from it.
 *
 * Recording pipeline (record mode only):
 *   - Beats are driven through native VS Code chrome (the Open Chat story picker)
 *     and the sidebar surfaces, so a thin EDITOR_BEATS manifest ({id,title,dwellMs})
 *     drives every chapter window — no in-webview narration popovers (the
 *     single-surface ChatSurface isn't the popover-hosting full SPA).
 *   - One ChapterRecorder clock spans every beat → <mp4>.chapters.json.
 *   - app.close() flushes the webm, then saveVideoAsMp4 transcodes to MP4
 *     (libx264/yuv420p/+faststart) → .artifacts/vscode-tour/vscode-tour.mp4.
 *   - Every beat is staged so the DOM visibly DIFFERS, then dwells until settled,
 *     then a numbered NN-<beat>.png is captured (the QA --frames input).
 *
 * Determinism contract (no flake allowed — a race is a bug, not a sleep):
 *   - No LLM. The backend runs `kitsoki web --flow stories/weather-report/
 *     flows/tour.yaml`, whose starlark_http_cassette replays all HTTP. No model,
 *     no socket.
 *   - Fixed VS Code 1.96.4, throwaway user-data + extensions dirs, fixed window
 *     size, VSCODE_* stripped (all in _helpers/launch.ts).
 *   - The backend port is auto-allocated by the extension (net :0), unique per
 *     run, so parallel runs never collide.
 *   - Readiness is asserted, never slept: backend health-poll (Backend.start),
 *     webview-guest descent (webviewFrame probes for a real element), and
 *     toHaveText/toBeVisible expectations with timeouts.
 *
 * Critical path asserted beat-by-beat (the scenarios kitsoki-ui-qa checks the
 * video against). Chat / trace / graph are independent surfaces that all follow
 * ONE backend session (surface decomposition):
 *   (a) "Kitsoki: Open Chat" goes straight to a story PICKER; picking the weather
 *       story starts the session and focuses Chat in VS Code's bottom panel;
 *   (b) maximizing the bottom panel keeps Chat out of the editor grid and exposes
 *       the full structured input;
 *   (d) a turn is driven in the bottom-panel chat and state advances
 *       (forecast → current-state=report, state-badge present; "Tokyo, Japan"
 *       renders);
 *   (h) surface decomposition — "Kitsoki: Open Trace" docks the Trace as its OWN
 *       webview in the "Kitsoki Surfaces" sidebar (a separate document/store from
 *       the chat) that discovers + follows the SAME session via
 *       runstatus.session.current and re-renders the driven timeline (host row);
 *   (i) likewise "Kitsoki: Open Graph" docks a standalone Graph surface that
 *       follows the session and marks the current station;
 *   (j) one backend — the chat + trace + graph webviews relay to a SINGLE spawned
 *       `kitsoki web` process (asserted via the host log: exactly one spawn);
 *   (e) close the surfaces sidebar while bottom-panel Chat remains visible; then
 *   (g) finale — app.yaml opens above the Chat panel, leaving source and
 *       conversation visible together (record only).
 *
 * Run (one-liner gate):  pnpm e2e
 *   ≡  KITSOKI_VSCODE_PACE=0 playwright test vscode-tour.e2e
 * Record (paced):        KITSOKI_VSCODE_PACE=1 pnpm playwright vscode-tour.e2e
 * Make targets:          make vscode-e2e-fast   /   make vscode-e2e
 *
 * Requires a built extension + embedded SPA: `make build && (cd tools/
 * vscode-kitsoki && pnpm build)`. packageExtension() asserts both are present.
 */
import { test, expect, type FrameLocator, type Locator, type Page } from '@playwright/test';
import * as fs from 'node:fs';
import * as path from 'node:path';
import * as os from 'node:os';
import { spawnSync } from 'node:child_process';
import {
  launchVSCode,
  packageExtension,
  type LaunchedVSCode,
} from './_helpers/launch';

const EXT_ROOT = path.resolve(__dirname, '..');
const REPO_ROOT = path.resolve(EXT_ROOT, '..', '..');
const STORY_DIR = path.join(REPO_ROOT, 'stories', 'weather-report');
const FLOW = path.join(STORY_DIR, 'flows', 'tour.yaml');
const APP_YAML = path.join(STORY_DIR, 'app.yaml');

const PACE = Number.parseInt(process.env.KITSOKI_VSCODE_PACE ?? '0', 10) || 0;
const RECORD = PACE >= 1;

// In assert mode every beat lands in .artifacts/vscode-e2e/ (the gate's scratch
// dir). In record mode the labeled NN-<beat>.png + the MP4 land in the canonical
// .artifacts/vscode-tour/ (the kitsoki-ui-qa --frames input).
// Optional VS Code color theme override (proves the embed themes NATIVELY off the
// editor theme — set e.g. KITSOKI_VSCODE_THEME="Default Light Modern" to capture a
// light-themed run). When set, record-mode artifacts land in a theme-suffixed dir
// so a light run never clobbers the canonical dark tour.
const THEME = process.env.KITSOKI_VSCODE_THEME?.trim() || '';
const THEME_SLUG = THEME ? '-' + THEME.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '') : '';
const GATE_DIR = path.join(REPO_ROOT, '.artifacts', 'vscode-e2e');
const TOUR_DIR = path.join(REPO_ROOT, '.artifacts', `vscode-tour${THEME_SLUG}`);
const ARTIFACT_DIR = RECORD ? TOUR_DIR : GATE_DIR;

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));
/** Paced dwell — a no-op in assert mode so the gate stays instant + deterministic. */
const dwell = (ms: number) => (RECORD ? sleep(ms * PACE) : Promise.resolve());

/**
 * Thin editor-beat manifest for beats OUTSIDE the webview (no popover narration
 * possible there). Each is a chapter window in the recorded MP4. The webview
 * beats are narrated by WEATHER_REPORT_TOUR_STEPS instead.
 */
const EDITOR_BEATS = {
  // "Kitsoki: Open Chat" starts the selected story in the bottom panel.
  pickerStart: { id: 'a-open-chat-picker', title: 'Open Chat → pick a story → bottom-panel Chat starts', dwellMs: 4000 },
  // Maximize the panel to expose the full structured form without creating an editor tab.
  panelMaximized: { id: 'b-chat-panel-maximized', title: 'Maximize bottom-panel Chat — full input, same session', dwellMs: 4000 },
  // Drive a turn in bottom-panel Chat — state advances to the report.
  driveTurn: { id: 'd-turn-driven', title: 'Drive a turn in bottom-panel Chat — state advances to the report', dwellMs: 4500 },
  // Surface decomposition: Trace and Graph each open as their OWN webview in the
  // "Kitsoki Surfaces" sidebar — a separate document/store from the chat editor
  // panel — and discover-and-follow the SAME backend session via
  // runstatus.session.current. These ride the manifest (no web-tour popover).
  tracePanel: { id: 'h-trace-panel', title: 'Trace in its own sidebar panel — same session', dwellMs: 4500 },
  graphPanel: { id: 'i-graph-panel', title: 'State graph in its own sidebar panel — same session', dwellMs: 4500 },
  // Close the surfaces sidebar while Chat remains in the bottom panel.
  chatMain: { id: 'e-chat-main', title: 'Close the surfaces sidebar — bottom-panel Chat remains', dwellMs: 3500 },
  // The finale: app.yaml above the bottom-panel conversation.
  splitEditor: { id: 'g-split-editor', title: 'Open the story file above the chat', dwellMs: 4000 },
} as const;

/**
 * ChapterRecorder — a single wall-clock spanning every beat, mapping each beat's
 * dwell window back to its id for the <mp4>.chapters.json sidecar. A local copy
 * (no cross-package import) keeps this spec self-contained; same shape as
 * tools/runstatus/tests/playwright/_helpers/server.ts ChapterRecorder.
 */
class ChapterRecorder {
  private readonly t0 = Date.now();
  private readonly chapters: Array<{
    index: number;
    id: string;
    label: string;
    start_ms: number;
    end_ms: number;
    source_ref: { kind: 'tour'; spec_path: string; step_id: string };
  }> = [];
  private open_: { id: string; label: string; startMs: number } | null = null;

  open(id: string, label: string): void {
    this.close();
    this.open_ = { id, label, startMs: Date.now() - this.t0 };
  }
  close(): void {
    if (!this.open_) return;
    const o = this.open_;
    this.chapters.push({
      index: this.chapters.length,
      id: o.id,
      label: o.label,
      start_ms: o.startMs,
      end_ms: Date.now() - this.t0,
      source_ref: {
        kind: 'tour',
        spec_path: 'tools/vscode-kitsoki/tests/vscode-tour.e2e.spec.ts',
        step_id: o.id,
      },
    });
    this.open_ = null;
  }
  list() {
    this.close();
    return this.chapters;
  }
  /** ms elapsed since the clock started (≈ the recording start) — used to trim
   *  the boot preamble (VS Code starting up before the first beat) off the head
   *  of the recorded video. */
  sinceStartMs(): number {
    return Date.now() - this.t0;
  }
}

/**
 * Transcode the Playwright-recorded .webm to a universally-playable H.264 MP4
 * (libx264 / yuv420p / +faststart / 30fps — same settings as the runstatus
 * saveVideoAsMp4 and scripts/webm-to-mp4.sh). MP4 plays inline in VS Code /
 * Keynote / Slack; the .webm never ships. Removes the webm on success. Returns
 * the MP4 path (or null if ffmpeg is unavailable / no webm was produced).
 */
function transcodeWebmToMp4(
  webm: string,
  mp4: string,
  headTrimMs = 0,
  crop?: { w: number; h: number },
): string | null {
  if (!fs.existsSync(webm)) return null;
  // Crop to the real content box first (top-left), dropping any recorder grey pad
  // bar the screen-clamped window left along the short edge(s); then 30fps + even-
  // dims for libx264.
  const cropF = crop ? `crop=${crop.w}:${crop.h}:0:0,` : '';
  const vf = `${cropF}fps=30,scale=trunc(iw/2)*2:trunc(ih/2)*2`;
  // -ss before -i drops the boot preamble (and its recorder grey bar) from the
  // head; the chapter sidecar is shifted by the same amount to stay in sync.
  const seek = headTrimMs > 250 ? ['-ss', (headTrimMs / 1000).toFixed(2)] : [];
  const r = spawnSync(
    'ffmpeg',
    ['-y', '-loglevel', 'error', ...seek, '-i', webm, '-vf', vf,
      '-c:v', 'libx264', '-preset', 'slow', '-crf', '20',
      '-pix_fmt', 'yuv420p', '-movflags', '+faststart', '-an', mp4],
    { encoding: 'utf8' },
  );
  if (r.status === 0) {
    fs.rmSync(webm, { force: true });
    return mp4;
  }
  console.log(`[video] ffmpeg transcode failed (keeping webm): ${r.stderr?.slice(0, 400)}`);
  return null;
}

/**
 * Dismiss the narration overlay in WHICHEVER webview still holds it. Once the
 * trace/graph/chat surfaces are open there are several `iframe.webview` hosts,
 * so scan every host and skip the tour where its overlay actually lives.
 * Best-effort + record-only.
 */
async function dismissTourEverywhere(win: Page): Promise<void> {
  if (!RECORD) return;
  const count = await win.locator('iframe.webview').count().catch(() => 0);
  for (let i = 0; i < count; i++) {
    for (const inner of ['iframe[title]', 'iframe[name="active-frame"]', 'iframe']) {
      const fl = win.frameLocator('iframe.webview').nth(i).frameLocator(inner).first();
      try {
        if (!(await fl.locator('[data-testid="tour-overlay"]').count())) continue;
        await fl
          .locator('body')
          .evaluate(() => (window as unknown as { __tourSkip?: () => void }).__tourSkip?.());
        await fl
          .locator('[data-testid="tour-overlay"]')
          .waitFor({ state: 'detached', timeout: 3000 })
          .catch(() => undefined);
      } catch {
        /* not this host */
      }
    }
  }
}

/**
 * Find the SPECIFIC webview guest frame that contains [data-testid="<testid>"],
 * scanning ALL `iframe.webview` hosts (the shared webviewFrame helper only probes
 * the .first() one, which breaks once multiple surfaces — chat panel + trace panel
 * + graph panel — are open at once). Returns the inner FrameLocator so callers can
 * assert against that surface's document in isolation.
 */
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
          /* try next inner / next webview host */
        }
      }
    }
    await sleep(250);
  }
  throw new Error(`no webview frame containing [data-testid="${testid}"] within ${timeoutMs}ms`);
}

/**
 * Widen the primary side bar by dragging its right-edge sash to `targetRightX`
 * (window CSS px). The picker flow drives the narrow Kitsoki sidebar surfaces, so
 * at the default ~257px width most of the frame is empty editor; widening lets the
 * surfaces dominate the frame. Record-only (the gate doesn't care about width, and
 * a drag has no place in the deterministic assert path). Best-effort — never fails
 * the gate by itself.
 */
async function widenSidebar(win: Page, targetRightX: number): Promise<void> {
  if (!RECORD) return;
  const sidebar = win.locator('.part.sidebar').first();
  const box = await sidebar.boundingBox().catch(() => null);
  if (!box) return;
  // The sash sits on the sidebar's right edge; grab it there and drag right. Use a
  // y well inside the sidebar body (below the pane headers) so we never catch a
  // header/tab instead of the sash.
  const y = box.y + Math.min(300, box.height / 2);
  await win.mouse.move(box.x + box.width, y);
  await win.mouse.down();
  await win.mouse.move(targetRightX, y, { steps: 16 });
  await win.mouse.up();
  // Park the cursor off the sash so its blue hover highlight doesn't bleed into the
  // captured frames (the empty editor area is inert — hovering it shows nothing).
  await win.mouse.move(targetRightX + 240, y);
}

/**
 * Toggle a sidebar view pane (by its header title) collapsed/expanded. Used to
 * give the focused surface the full sidebar height — and to make the trace beat
 * and graph beat visually distinct (collapse the other one). Pane headers are
 * workbench chrome (not inside a webview), so this click is reliable.
 */
async function clickPaneHeader(win: Page, title: string): Promise<void> {
  await win
    .locator('.pane-header')
    .filter({ hasText: new RegExp(`^\\s*${title}\\b`, 'i') })
    .first()
    .click()
    .catch(() => undefined);
}

/**
 * Locate a view pane by its header title.
 */
function paneByTitle(win: Page, title: string) {
  return win
    .locator('.pane')
    .filter({ has: win.locator('.pane-header').filter({ hasText: new RegExp(`^\\s*${title}\\b`, 'i') }) })
    .first();
}

/**
 * Click an inline title-bar action of a sidebar view pane (the `view/title`
 * navigation-group buttons). They render on hover / when the pane is active, so
 * hover the header first. `actionLabel` matches the action's aria-label (the
 * command title). Best-effort — never fails the gate by itself.
 */
async function clickViewTitleAction(win: Page, paneTitle: string, actionLabel: string): Promise<void> {
  const pane = paneByTitle(win, paneTitle);
  await pane.locator('.pane-header').hover().catch(() => undefined);
  await pane
    .locator(`.actions-container a[aria-label*="${actionLabel}" i], .actions-container a[title*="${actionLabel}" i]`)
    .first()
    .click()
    .catch(() => undefined);
}

test('vscode tour e2e — load, render, drive, trace (no-LLM, deterministic)', async () => {
  test.setTimeout(300_000);

  // Sanity: the no-LLM fixtures must exist (fail loudly, not as a blank webview).
  for (const p of [FLOW, STORY_DIR, APP_YAML, path.join(STORY_DIR, 'cassettes', 'tour.http.yaml')]) {
    if (!fs.existsSync(p)) throw new Error(`missing no-LLM fixture: ${p}`);
  }

  const tmpRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'vscode-kitsoki-e2e-'));
  const workspace = path.join(tmpRoot, 'workspace');
  fs.mkdirSync(path.join(workspace, '.vscode'), { recursive: true });

  // Inject extension settings: point Backend at the weather-report no-LLM flow.
  fs.writeFileSync(
    path.join(workspace, '.vscode', 'settings.json'),
    JSON.stringify(
      {
        'kitsoki.flow': FLOW,
        'kitsoki.storiesDir': STORY_DIR,
        'kitsoki.binaryPath': fs.existsSync(path.join(REPO_ROOT, 'bin', 'kitsoki'))
          ? path.join(REPO_ROOT, 'bin', 'kitsoki')
          : '',
        // Keep the recorded editor frames clean: suppress VS Code's "Git
        // repositories found in parent folders" toast (the story file opens from
        // outside the throwaway workspace), the minimap, and other chrome noise.
        'git.enabled': false,
        'git.openRepositoryInParentFolders': 'never',
        'editor.minimap.enabled': false,
        'workbench.tips.enabled': false,
        'workbench.startupEditor': 'none',
        'editor.fontSize': 13,
        // Optional theme override — proves the embed themes natively off the editor
        // theme (the SPA has no theme system of its own; it reads --vscode-* vars).
        ...(THEME ? { 'workbench.colorTheme': THEME } : {}),
      },
      null,
      2,
    ),
  );

  const extensionsDir = packageExtension(EXT_ROOT, path.join(tmpRoot, 'extensions'));

  // Tee the extension host's OutputChannel to a file the spec can read.
  fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
  const hostLog = path.join(ARTIFACT_DIR, 'extension-host.log');
  fs.writeFileSync(hostLog, '');
  process.env.KITSOKI_E2E_LOG = hostLog;

  // Capture webview console errors to a file so the recording's quality can be
  // audited (e.g. a stray 404 / CSP refusal that would mar a frame).
  const consoleLog = path.join(ARTIFACT_DIR, 'webview-console.log');
  fs.writeFileSync(consoleLog, '');

  const videoDir = path.join(ARTIFACT_DIR, 'video');

  let shotIdx = 0;
  let launched: LaunchedVSCode | undefined;
  const chapters = new ChapterRecorder();
  // Filled once the workbench is up + sized: the head of the recording that is
  // pure VS Code boot (and the brief window-resize-to-record-size settle, where
  // the recorder pads the not-yet-full window with a grey bar). Trimmed off the
  // MP4 so the demo opens on the first beat, not on a booting editor with a bar.
  let bootTrimMs = 0;

  try {
    launched = await launchVSCode({
      workspace,
      extensionsDir,
      userDataDir: path.join(tmpRoot, 'user-data'),
      size: { width: 1400, height: 900 },
      ...(RECORD ? { videoDir } : {}),
    });
    bootTrimMs = chapters.sinceStartMs();
    const { win } = launched;

    win.on('console', (m) => {
      if (m.type() === 'error') {
        const line = `[webview console.error] ${m.text()}`;
        console.log(line);
        try {
          fs.appendFileSync(consoleLog, line + '\n');
        } catch {
          /* best-effort */
        }
      }
    });
    win.on('pageerror', (e) => {
      const line = `[webview pageerror] ${e.message}`;
      console.log(line);
      try {
        fs.appendFileSync(consoleLog, line + '\n');
      } catch {
        /* best-effort */
      }
    });
    // Record non-2xx HTTP responses so a 404 that mars a frame is traceable to a
    // concrete URL (the SPA's console.error text doesn't carry it).
    win.on('response', (r) => {
      const s = r.status();
      if (s >= 400) {
        const line = `[webview http ${s}] ${r.url()}`;
        try {
          fs.appendFileSync(consoleLog, line + '\n');
        } catch {
          /* best-effort */
        }
      }
    });

    const shot = async (label: string) => {
      const n = String(++shotIdx).padStart(2, '0');
      const p = path.join(ARTIFACT_DIR, `${n}-${label}.png`);
      fs.mkdirSync(ARTIFACT_DIR, { recursive: true });
      await win.screenshot({ path: p }).catch(() => undefined);
      return p;
    };

    // ── (a) Open Chat → story PICKER → start → bottom panel reflects it ────────
    // Chat is contributed to VS Code's bottom panel beside Terminal / Ports /
    // Playwright. It never creates an editor tab. Open Chat starts the selected
    // story and focuses that panel view.
    await win.waitForSelector('.monaco-workbench', { timeout: 60_000 });
    const openChat = await runPaletteCommand(win, ['>Kitsoki: Open Chat']);
    expect(openChat, '"Kitsoki: Open Chat" command available').toBe(true);
    const picked = await drivePicker(win, 'weather');
    expect(picked, 'Open Chat story picker offered the weather story').toBe(true);
    await dwell(1000);

    await expect(
      win.locator('.part.panel').getByRole('tab', { name: /^Kitsoki\b/i }),
      'the Kitsoki Chat view opened in VS Code\'s bottom panel',
    ).toBeVisible({ timeout: 30_000 });
    await expect(
      win.locator('.part.editor .tab.active').filter({ hasText: /^\s*Kitsoki\s*$/i }),
      'opening bottom-panel chat did not create a Kitsoki editor tab',
    ).toHaveCount(0);

    // The started session lands in the panel ChatSurface immediately
    // (current-session discovery on mount + the live subscribeCurrentSession seam).
    // This is also the bundle+CSP+relay+backend round-trip in one assertion: a
    // rendered session means the relay works.
    const chatFrame: FrameLocator = await surfaceFrame(win, 'surface-chat', 45_000);
    await expect(
      chatFrame.locator('[data-testid="surface-chat"]'),
      'the bottom-panel Chat surface mounted (its own webview, BridgeTransport relay)',
    ).toBeVisible({ timeout: 30_000 });
    await expect(
      chatFrame.locator('[data-testid="current-state"]'),
      'a fresh session opens in the lobby room',
    ).toHaveText('lobby', { timeout: 30_000 });
    if (RECORD) {
      chapters.open(EDITOR_BEATS.pickerStart.id, EDITOR_BEATS.pickerStart.title);
      await dwell(EDITOR_BEATS.pickerStart.dwellMs);
    }
    await shot('a-open-chat-picker');

    // ── (b) Maximize the bottom panel; chat remains outside the editor grid ────
    await runPaletteCommand(win, ['>View: Toggle Maximized Panel']);
    await dwell(800);
    await expect(
      win.locator('.part.panel').getByRole('tab', { name: /^Kitsoki\b/i }),
      'the maximized Chat stays in the bottom panel',
    ).toBeVisible({ timeout: 15_000 });
    const editorChat: FrameLocator = await surfaceFrame(win, 'surface-chat', 45_000);
    await dismissTourEverywhere(win);
    await expect(
      editorChat.locator('[data-testid="surface-chat"]'),
      'the panel chat remains mounted after maximizing',
    ).toBeVisible({ timeout: 30_000 });
    await expect(
      editorChat.locator('[data-testid="current-state"]'),
      'the panel chat remains on the same session',
    ).toHaveText('lobby', { timeout: 30_000 });
    // Tall editor → no collapse: the structured forecast form shows directly and
    // there is NO disclosure icon (nothing is hidden).
    const forecastForm = editorChat.locator('form[data-intent="forecast"]');
    await expect(
      forecastForm,
      'the maximized panel shows the full structured input',
    ).toBeVisible({ timeout: 15_000 });
    await expect(
      editorChat.locator('[data-testid="input-disclose"]'),
      'no disclosure in the maximized panel — the actions already fit',
    ).toHaveCount(0);
    if (RECORD) {
      chapters.open(EDITOR_BEATS.panelMaximized.id, EDITOR_BEATS.panelMaximized.title);
      await dwell(EDITOR_BEATS.panelMaximized.dwellMs);
    }
    await shot('b-chat-panel-maximized');

    // ── (d) Drive a turn in the bottom-panel chat → state advances ────────────
    // The param composer is a wrapping <textarea> now (not a single-line input).
    await forecastForm.locator('textarea').fill('Tokyo');
    await dwell(700);
    await forecastForm
      .locator('button[type="submit"]')
      .evaluate((button) => (button as HTMLButtonElement).click());
    await expect(
      editorChat.locator('[data-testid="current-state"]'),
      'driven turn advances current-state lobby → report',
    ).toHaveText('report', { timeout: 30_000 });
    await expect(
      editorChat.locator('[data-testid="state-badge"]'),
      'state-badge present after the driven turn',
    ).toBeVisible({ timeout: 10_000 });
    await expect(
      editorChat.locator('[data-testid="chat-transcript"]').getByText('Tokyo, Japan'),
      'forecast report rendered (cassette replay, no LLM)',
    ).toBeVisible({ timeout: 15_000 });
    if (RECORD) {
      // Scroll the resolved place ("Tokyo, Japan") to the top of the chat column so
      // the report's "paper" card is on-camera and its values aren't clipped.
      await editorChat
        .locator('[data-testid="chat-transcript"]')
        .getByText('Tokyo, Japan')
        .first()
        .evaluate((el) => el.scrollIntoView({ block: 'start', behavior: 'instant' as ScrollBehavior }))
        .catch(() => undefined);
      await dwell(400);
      chapters.open(EDITOR_BEATS.driveTurn.id, EDITOR_BEATS.driveTurn.title);
      await dwell(EDITOR_BEATS.driveTurn.dwellMs);
    }
    await shot('d-turn-driven');

    // intent-btn-* control path: the report room exposes a `back` action button.
    const backBtn = editorChat.locator('[data-testid="intent-btn-back"]').first();
    await expect(backBtn, 'intent-btn-back control present in report room').toBeVisible({
      timeout: 15_000,
    });

    // ── (h) Surface decomposition: Trace in its OWN panel, same session ───────
    // The headline of this rework. "Kitsoki: Open Trace" reveals a webview view
    // docked in the Kitsoki Surfaces activity-bar container — a SEPARATE document
    // (own Pinia store, own Relay) from the chat panel. It has no chat to start a session, so it
    // discovers and follows the active one via runstatus.session.current, then
    // renders the SAME driven trace. Proves N-windows / one-session fan-out.
    const traceOpened = await runPaletteCommand(win, ['>Kitsoki: Open Trace']);
    expect(traceOpened, '"Kitsoki: Open Trace" command available').toBe(true);
    await dwell(600);
    const traceFrame: FrameLocator = await surfaceFrame(win, 'surface-trace', 30_000);
    // Collapse the Graph pane so Trace fills the sidebar height (its event ROWS,
    // not just header + filters, are on-camera) and this beat is visually distinct
    // from the graph beat.
    await clickPaneHeader(win, 'Graph');
    await dwell(500);
    await expect(
      traceFrame.locator('[data-testid="surface-trace"]'),
      'Trace panel mounts the standalone trace surface (its own webview)',
    ).toBeVisible({ timeout: 30_000 });
    await expect(
      traceFrame.locator('[data-testid="trace-timeline"]'),
      'standalone Trace surface followed the active session and rendered the timeline',
    ).toBeVisible({ timeout: 30_000 });
    await expect(
      traceFrame
        .locator('[data-testid="trace-timeline"]')
        .locator('.trace-timeline__row:has([data-subsystem="host"])')
        .first(),
      'standalone Trace shows the SAME driven turn (host.starlark.run row) — shared session',
    ).toBeVisible({ timeout: 30_000 });
    if (RECORD) {
      chapters.open(EDITOR_BEATS.tracePanel.id, EDITOR_BEATS.tracePanel.title);
      await dwell(EDITOR_BEATS.tracePanel.dwellMs);
    }
    await shot('h-trace-panel');

    // ── (i) Surface decomposition: Graph in its OWN panel, same session ───────
    const graphOpened = await runPaletteCommand(win, ['>Kitsoki: Open Graph']);
    expect(graphOpened, '"Kitsoki: Open Graph" command available').toBe(true);
    // Collapse the Trace pane so the Graph diagram fills the sidebar — a distinct
    // frame from the trace beat (the Graph focus above re-expanded its pane).
    await clickPaneHeader(win, 'Trace');
    await dwell(500);
    const graphFrame: FrameLocator = await surfaceFrame(win, 'surface-graph', 30_000);
    await expect(
      graphFrame.locator('[data-testid="surface-graph"]'),
      'Graph panel mounts the standalone graph surface (its own webview)',
    ).toBeVisible({ timeout: 30_000 });
    await expect(
      graphFrame.locator('[data-testid="trace-diagram"]'),
      'standalone Graph surface followed the active session and rendered the diagram',
    ).toBeVisible({ timeout: 30_000 });
    await expect(
      graphFrame.locator('[data-testid="diagram-current-station"]').first(),
      'standalone Graph marks the current station — shared session',
    ).toBeVisible({ timeout: 30_000 });
    if (RECORD) {
      chapters.open(EDITOR_BEATS.graphPanel.id, EDITOR_BEATS.graphPanel.title);
      await dwell(EDITOR_BEATS.graphPanel.dwellMs);
    }
    await shot('i-graph-panel');

    // ── (j) One backend across every surface ─────────────────────────────────
    // Bottom-panel Chat + Trace + Graph are three webviews, but the
    // host spawns exactly ONE `kitsoki web` process — they all relay to it. Assert
    // the extension host log shows a single backend spawn (no per-surface backend).
    const spawnCount = (fs.readFileSync(hostLog, 'utf8').match(/\[backend\] spawn:/g) ?? []).length;
    expect(spawnCount, 'exactly one backend process serves all three surfaces').toBe(1);

    // ── (e) Close the surfaces sidebar; chat remains in the bottom panel ───────
    await dismissTourEverywhere(win);
    if (RECORD) {
      await runPaletteCommand(win, ['>View: Close Primary Side Bar', '>View: Toggle Primary Side Bar Visibility']);
      await dwell(700);
    }
    await expect(
      win.locator('.part.panel').getByRole('tab', { name: /^Kitsoki\b/i }),
      'closing the surfaces sidebar leaves bottom-panel chat visible',
    ).toBeVisible({ timeout: 15_000 });
    if (RECORD) {
      chapters.open(EDITOR_BEATS.chatMain.id, EDITOR_BEATS.chatMain.title);
      await dwell(EDITOR_BEATS.chatMain.dwellMs);
    }
    await shot('e-chat-main');

    // ── (g) Finale: the story file above the bottom-panel chat (record only) ──
    if (RECORD) {
      await openFileAbove(win, APP_YAML);
      await expect(
        win.locator('.monaco-editor').filter({ hasText: 'weather-report' }).first(),
        'app.yaml open in a split ABOVE the Kitsoki chat panel',
      ).toBeVisible({ timeout: 15_000 });
      chapters.open(EDITOR_BEATS.splitEditor.id, EDITOR_BEATS.splitEditor.title);
      await dwell(EDITOR_BEATS.splitEditor.dwellMs);
      await shot('g-split-editor');
    }
  } finally {
    chapters.close();
    if (launched) await launched.app.close().catch(() => undefined);

    // Record mode: app.close() has flushed the webm — transcode it to the
    // canonical MP4 and write the chapter sidecar beside it.
    if (RECORD) {
      try {
        const webms = fs.existsSync(videoDir)
          ? fs.readdirSync(videoDir).filter((f) => f.endsWith('.webm')).map((f) => path.join(videoDir, f))
          : [];
        // Pick the most-recently-modified webm (this run's recording).
        webms.sort((a, b) => fs.statSync(b).mtimeMs - fs.statSync(a).mtimeMs);
        const webm = webms[0];
        const mp4 = path.join(TOUR_DIR, 'vscode-tour.mp4');
        if (webm) {
          const out = transcodeWebmToMp4(webm, mp4, bootTrimMs, launched?.viewport);
          if (out) {
            console.log(`[video] ${out} (trimmed ${bootTrimMs}ms boot preamble)`);
            const sidecar = `${out}.chapters.json`;
            // Shift chapter timings to match the trimmed head so the sidecar
            // stays aligned with the MP4.
            const shifted = chapters.list().map((c) => ({
              ...c,
              start_ms: Math.max(0, c.start_ms - bootTrimMs),
              end_ms: Math.max(0, c.end_ms - bootTrimMs),
            }));
            fs.writeFileSync(sidecar, JSON.stringify(shifted, null, 2) + '\n');
            console.log(`[chapters] ${sidecar} (${shifted.length})`);
          } else {
            console.log(`[video] transcode failed; webm left at ${webm}`);
          }
        } else {
          console.log(`[video] no webm produced in ${videoDir}`);
        }
      } catch (e) {
        console.log(`[video] post-processing error: ${e instanceof Error ? e.message : String(e)}`);
      }
    }

    fs.rmSync(tmpRoot, { recursive: true, force: true });
  }
});

/**
 * Open VS Code's quick input (command palette `Cmd+Shift+P` or quick-open `Cmd+P`)
 * ROBUSTLY and return its combobox. A freshly-resolved webview steals keyboard
 * focus on mount, so the FIRST chord can land in the webview iframe and be
 * swallowed before VS Code sees it (an intermittent "the palette never opened"
 * flake). Rather than sleep past it, re-press the chord — bounded — until the
 * combobox actually appears: a race converted into a deterministic poll. Returns
 * the input locator, or null if it never opened (the caller asserts on that).
 */
async function openQuickInput(win: Page, openKeys: string): Promise<Locator | null> {
  const input = win.getByRole('combobox', { name: 'input' });
  for (let attempt = 0; attempt < 5; attempt++) {
    await win.keyboard.press(openKeys);
    const opened = await input
      .waitFor({ timeout: 2000 })
      .then(() => true)
      .catch(() => false);
    if (opened) return input;
    // The chord was swallowed (webview had focus). Reset any half-state and let
    // focus settle back to the workbench, then re-press.
    await win.keyboard.press('Escape').catch(() => undefined);
    await sleep(250);
  }
  return null;
}

/**
 * Run a Command Palette command by title. Tries each candidate query in order,
 * committing only when the palette actually has a match (so a missing command
 * never dead-presses Enter on "No matching results"). Keep the leading ">" so the
 * palette stays in command mode (a bare query searches files instead). Returns
 * true once a command was committed.
 */
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

/**
 * Drive the QuickPick that "Kitsoki: Open Chat" opens (the story picker): type
 * `query`, confirm a row matched, and Enter to start that story. This is the SAME
 * `.quick-input-widget` the command palette uses, so it's driven the same way.
 * Returns false (and dismisses the picker) when nothing matched.
 */
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

/**
 * Open a file in a split editor ABOVE the Kitsoki panel: open it (it lands in the
 * active group, atop the retained Kitsoki webview), then move it into the group
 * ABOVE so the chat stays visible below it — story source on top, conversation
 * underneath, stacked vertically in one column.
 */
async function openFileAbove(win: Page, absPath: string): Promise<void> {
  await openFileInEditor(win, absPath);
  await runPaletteCommand(win, [
    '>View: Move Editor into Above Group',
    '>Move Editor into Above Group',
  ]);
  await sleep(800);
}

/**
 * Open a file in the editor via the workbench Quick Open (Cmd/Ctrl+P), the most
 * reliable cross-platform path. Falls back to typing the absolute path. The
 * editor tab + content are asserted by the caller.
 */
async function openFileInEditor(win: Page, absPath: string): Promise<void> {
  const isMac = process.platform === 'darwin';
  // Quick Open via the same robust open. The widget hosts two
  // inputs (a hidden check-all checkbox + the real text combobox); openQuickInput
  // targets the combobox precisely, avoiding a strict-mode violation.
  const input = await openQuickInput(win, isMac ? 'Meta+P' : 'Control+P');
  if (!input) return;
  // The workspace folder is the throwaway tmp dir, so the story file isn't under
  // it; type the absolute path which Quick Open resolves directly.
  await input.fill(absPath);
  await sleep(800);
  await win.keyboard.press('Enter');
  await sleep(1000);
}
