// launch.ts — typed, thin port of the VS Code launch mechanism proven in
// .artifacts/vscode-poc/record-vscode-poc.mjs. Both the e2e driving spec and
// the demo recorder import this; keep it dependency-light and side-effect free
// at import time.
//
// Proven facts baked in here (see .artifacts/vscode-poc/NOTES.md):
//  - download+launch VS Code 1.96.4 via @vscode/test-electron + Playwright _electron.
//  - STRIP all VSCODE_* env vars before launch (inherited ones hang the window
//    and break custom webviews).
//  - firstWindow() is flaky; poll app.windows() for `.monaco-workbench` instead.
//  - webview descent is `iframe.webview.ready` >>> `iframe[title]`.

import { _electron, type ElectronApplication, type Page, type FrameLocator } from 'playwright';
import { downloadAndUnzipVSCode } from '@vscode/test-electron';
import * as fs from 'node:fs';
import * as path from 'node:path';
import * as os from 'node:os';

/** Pinned VS Code version proven by the PoC. */
export const VSCODE_VERSION = '1.96.4';

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

function freshDir(p: string): string {
  fs.rmSync(p, { recursive: true, force: true });
  fs.mkdirSync(p, { recursive: true });
  return p;
}

/**
 * Stage the BUILT extension (package.json + dist/ + media/) into a throwaway
 * extensions dir as `<extensionsDir>/<publisher>.<name>-<version>/`, the layout
 * VS Code discovers an unpacked extension under `--extensions-dir`. Returns the
 * extensions dir to pass to {@link launchVSCode}. Asserts the compiled host
 * entry and the staged SPA are present so a missing `pnpm build` fails loudly
 * here instead of as an inscrutable blank webview later.
 *
 * `extensionRoot` is the tools/vscode-kitsoki dir (where package.json lives).
 */
export function packageExtension(extensionRoot: string, extensionsDir: string): string {
  const pkg = JSON.parse(fs.readFileSync(path.join(extensionRoot, 'package.json'), 'utf8')) as {
    name: string;
    version: string;
    publisher: string;
  };
  const entry = path.join(extensionRoot, 'dist', 'extension.js');
  const spa = path.join(extensionRoot, 'media', 'spa', 'index.html');
  if (!fs.existsSync(entry)) {
    throw new Error(`extension not built: ${entry} missing — run 'pnpm build' first`);
  }
  if (!fs.existsSync(spa) || fs.statSync(spa).size < 10_000) {
    throw new Error(
      `embedded SPA missing/placeholder: ${spa} — run 'make build' (or build tools/runstatus) then 'pnpm build'`,
    );
  }
  freshDir(extensionsDir);
  const dest = path.join(extensionsDir, `${pkg.publisher}.${pkg.name}-${pkg.version}`);
  fs.mkdirSync(dest, { recursive: true });
  fs.copyFileSync(path.join(extensionRoot, 'package.json'), path.join(dest, 'package.json'));
  fs.cpSync(path.join(extensionRoot, 'dist'), path.join(dest, 'dist'), { recursive: true });
  fs.cpSync(path.join(extensionRoot, 'media'), path.join(dest, 'media'), { recursive: true });
  return extensionsDir;
}

export interface LaunchOptions {
  /** Folder opened as the workspace (last positional arg to VS Code). */
  workspace: string;
  /** Directory the recordVideo .webm is written to. Omit to disable recording. */
  videoDir?: string;
  /** Extensions dir; defaults to a throwaway temp dir. Install the built extension here. */
  extensionsDir?: string;
  /** User data dir; defaults to a throwaway temp dir. */
  userDataDir?: string;
  /** Window size for recordVideo. Defaults to 1280x800. */
  size?: { width: number; height: number };
  /** Launch timeout ms. Defaults to 120000. */
  timeout?: number;
}

export interface LaunchedVSCode {
  app: ElectronApplication;
  /** The workbench window (one whose .monaco-workbench exists). */
  win: Page;
}

/**
 * Download (cached) + launch real VS Code with the proven flags and return the
 * app plus the workbench window. Caller is responsible for `app.close()`.
 */
export async function launchVSCode(opts: LaunchOptions): Promise<LaunchedVSCode> {
  const tmpRoot = path.join(os.tmpdir(), 'vscode-kitsoki-test');
  const userDataDir = freshDir(opts.userDataDir ?? path.join(tmpRoot, 'user-data'));
  const extensionsDir = opts.extensionsDir
    ? opts.extensionsDir
    : freshDir(path.join(tmpRoot, 'extensions'));
  const size = opts.size ?? { width: 1280, height: 800 };

  const executablePath = await downloadAndUnzipVSCode(VSCODE_VERSION);

  // CRITICAL: strip all VSCODE_* env vars (inherited ones hang the launch).
  const env: Record<string, string> = {};
  for (const [k, v] of Object.entries(process.env)) {
    if (v === undefined) continue;
    if (/^VSCODE_/i.test(k)) continue;
    env[k] = v;
  }

  const args = [
    '--no-sandbox',
    '--disable-gpu-sandbox',
    '--disable-updates',
    '--skip-welcome',
    '--skip-release-notes',
    '--disable-workspace-trust',
    '--disable-telemetry',
    `--user-data-dir=${userDataDir}`,
    `--extensions-dir=${extensionsDir}`,
    opts.workspace,
  ];

  const app = await _electron.launch({
    executablePath,
    env,
    args,
    timeout: opts.timeout ?? 120_000,
    ...(opts.videoDir ? { recordVideo: { dir: freshDir(opts.videoDir), size } } : {}),
  });

  const win = await acquireWorkbench(app);

  // Match the OS window to the recordVideo size. From a fresh user-data-dir VS
  // Code restores its OWN default window bounds (narrower than `size`), but the
  // video is captured at `size` — Playwright pads the shortfall with the
  // recorder's grey background, i.e. a solid bar down an edge of the .mp4. That
  // bar is invisible in window screenshots (which capture the window directly)
  // and shipped unseen until `make vscode-qa` caught it. Force the window to
  // exactly `size` so the workbench fills the recorded frame edge-to-edge.
  if (opts.videoDir) {
    await app
      .evaluate(({ BrowserWindow }, s) => {
        const w = BrowserWindow.getAllWindows()[0];
        if (!w) return;
        if (w.isMaximized()) w.unmaximize();
        w.setBounds({ x: 0, y: 0, width: s.width, height: s.height });
      }, size)
      .catch(() => undefined);
    // Let the workbench relayout to the new bounds before beats are captured.
    await win.waitForTimeout(400);
  }

  return { app, win };
}

/**
 * Poll app.windows() for the workbench window (firstWindow() is flaky because
 * VS Code spawns background windows). Falls back to firstWindow().
 */
export async function acquireWorkbench(app: ElectronApplication): Promise<Page> {
  let win: Page | null = null;
  for (let i = 0; i < 120; i++) {
    for (const w of app.windows()) {
      try {
        if (await w.locator('.monaco-workbench').count()) {
          win = w;
          break;
        }
      } catch {
        /* not ready */
      }
    }
    if (win) break;
    await sleep(500);
  }
  if (!win) win = await app.firstWindow({ timeout: 30_000 });
  await win.waitForSelector('.monaco-workbench', { timeout: 60_000 });
  return win;
}

/**
 * Descend into a VS Code webview's guest document. Proven chain on 1.96.4:
 * `iframe.webview.ready` (outer host) >>> `iframe[title]` (inner active-frame).
 * Tries a small fallback matrix and returns the first FrameLocator whose guest
 * contains `probe` (default: any element).
 */
export async function webviewFrame(
  win: Page,
  probe?: { selector: string; hasText?: string },
  timeoutMs = 8000,
): Promise<FrameLocator> {
  const outers = ['iframe.webview.ready', 'iframe.webview'];
  const inners = ['iframe[title]', 'iframe[name="active-frame"]', 'iframe'];
  const deadline = Date.now() + timeoutMs;
  let last: FrameLocator | null = null;
  while (Date.now() < deadline) {
    for (const outer of outers) {
      for (const inner of inners) {
        const fl = win.frameLocator(outer).frameLocator(inner);
        last = fl;
        try {
          const target = probe
            ? fl.locator(probe.selector, probe.hasText ? { hasText: probe.hasText } : undefined).first()
            : fl.locator('body').first();
          await target.waitFor({ timeout: 1000 });
          return fl;
        } catch {
          /* try next combo */
        }
      }
    }
    await sleep(250);
  }
  if (last) return last;
  throw new Error('could not locate VS Code webview guest frame');
}
