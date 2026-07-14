// webview.ts — renders the bundled singlefile SPA into a VS Code webview and
// wires the postMessage relay to the shared backend.
//
// Chat is a WebviewView contributed to VS Code's bottom panel, alongside the
// Terminal / Ports / Playwright views. Trace and Graph are WebviewViews in the
// activity bar. mountSpa() is the shared path: relay wiring + nonce/CSP + backend
// start. No surface uses an editor-area WebviewPanel.

import * as vscode from 'vscode';
import * as fs from 'node:fs';
import * as crypto from 'node:crypto';
import { Relay, type InboundEnvelope, type OutboundEnvelope } from './relay';
import type { Backend } from './backend';

// The webview-only THEME_SHIM was retired: the SPA now consumes VS Code theme
// variables natively. Every component's colors resolve through the `--k-*` token
// layer (tools/runstatus/src/theme.css), each a `var(--vscode-*, <fallback>)`
// chain — so inside a webview the editor theme drives the UI directly (and tracks
// live theme switches with zero extension round-trip), while a plain browser falls
// back to the original palette. The agent room-view "paper" card now follows the
// editor surface via `--k-paper-*` instead of being force-darkened here.

/** Which kitsoki surface a webview hosts. Injected as `window.__KITSOKI_SURFACE`
 * so the SPA mounts the right single-surface view (chat / trace / graph). Chat
 * mounts as the bottom-panel ChatSurface; trace and graph mount in the activity
 * bar. All share the one backend session. */
export type Surface = 'chat' | 'trace' | 'graph';

/** Read the bundled singlefile SPA and inject a per-render CSP + nonce + theme.
 * `surface` selects the decomposed surface mounted in this webview. */
export function renderSpaHtml(
  webview: vscode.Webview,
  extensionUri: vscode.Uri,
  surface: Surface,
): string {
  const indexPath = vscode.Uri.joinPath(extensionUri, 'media', 'spa', 'index.html').fsPath;
  let html = fs.readFileSync(indexPath, 'utf8');
  const nonce = crypto.randomBytes(16).toString('base64');

  const csp = [
    `default-src 'none'`,
    `script-src 'nonce-${nonce}'`,
    // style-src uses 'unsafe-inline' ALONE (no nonce). The SPA is Vue: it
    // injects <style> elements at runtime with no nonce, and a nonce in
    // style-src makes the browser IGNORE 'unsafe-inline' — so a nonce here
    // would refuse every runtime-injected style and strip the UI's styling.
    // Inline styles cannot execute code, so 'unsafe-inline' is the safe and
    // standard webview posture; the script nonce stays strict.
    `style-src 'unsafe-inline'`,
    `img-src ${webview.cspSource} data: blob:`,
    `font-src ${webview.cspSource}`,
  ].join('; ');

  // Add a nonce to every inline <script> the singlefile bundle inlines (the
  // script-src policy requires it). Styles need no nonce under 'unsafe-inline'.
  html = html.replace(/<script(?![^>]*\bnonce=)/g, `<script nonce="${nonce}"`);

  const cspMeta = `<meta http-equiv="Content-Security-Policy" content="${csp}">`;
  // Surface marker — read by the SPA on boot to mount one decomposed view. Uses
  // the SAME nonce as every other script so script-src lets it run.
  const surfaceTag = `<script nonce="${nonce}">window.__KITSOKI_SURFACE=${JSON.stringify(surface)};</script>`;
  const head = `${cspMeta}\n${surfaceTag}`;

  if (/<head[^>]*>/i.test(html)) {
    html = html.replace(/<head[^>]*>/i, (m) => `${m}\n${head}`);
  } else {
    html = `${head}\n${html}`;
  }
  return html;
}

/** Minimal error page shown when the backend never comes up. */
export function renderError(message: string): string {
  return `<!DOCTYPE html><html><head><meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline';">
</head><body style="font-family: sans-serif; padding: 1rem; color: var(--vscode-errorForeground);">
<h3>Kitsoki backend failed to start</h3>
<pre>${message.replace(/[<&]/g, (c) => (c === '<' ? '&lt;' : '&amp;'))}</pre>
<p>Run <code>Kitsoki: Restart Backend</code> after fixing the binary path / settings.</p>
</body></html>`;
}

/**
 * Wire a webview to the shared backend: a postMessage relay (host side of the
 * BridgeTransport protocol), then bring the backend up and render the SPA. The
 * returned Disposable tears down the relay + message subscription. Shared by the
 * panel and activity-bar webview surfaces so they can never drift.
 */
export function mountSpa(
  webview: vscode.Webview,
  extensionUri: vscode.Uri,
  backend: Backend,
  out: vscode.OutputChannel,
  surface: Surface,
): vscode.Disposable {
  const mediaRoot = vscode.Uri.joinPath(extensionUri, 'media');
  webview.options = { enableScripts: true, localResourceRoots: [mediaRoot] };

  const relay = new Relay({
    base: '', // set once the backend is ready (below)
    post: (env: OutboundEnvelope) => {
      void webview.postMessage(env);
    },
    log: (line) => out.appendLine(line),
  });

  const sub = webview.onDidReceiveMessage((msg: InboundEnvelope) => relay.handle(msg));

  // Point the relay at `base` and (re)boot the SPA against it. Shared by the
  // initial start and the restart path so a restart can never drift from first
  // mount. resetStreams() first because a restart lands a NEW port: the relay's
  // long-lived SSE channels captured the OLD base at open time, so they must be
  // torn down before the rebooted SPA re-opens them against the new port.
  const render = async (base: string): Promise<void> => {
    relay.resetStreams();
    relay.setBase(base);
    webview.html = renderSpaHtml(webview, extensionUri, surface);
  };

  void backend
    .start()
    .then(render)
    .catch((err: Error) => {
      out.appendLine(`[webview] backend start failed: ${err.message}`);
      webview.html = renderError(err.message);
    });

  // A backend restart hands every mounted webview a new port: re-point the relay
  // and reboot the SPA so it reconnects there instead of "fetch failed"-ing
  // against the dead old port.
  const restartSub = backend.onDidRestart((base) => {
    void render(base).catch((err: Error) => {
      out.appendLine(`[webview] re-render after backend restart failed: ${err.message}`);
    });
  });

  return new vscode.Disposable(() => {
    relay.dispose();
    sub.dispose();
    restartSub.dispose();
  });
}

/**
 * WebviewViewProvider for a panel or activity-bar surface. resolveWebviewView
 * mounts the SPA with the surface marker; the SPA re-hydrates frontend-side on
 * each (re)resolve / visibility change.
 *
 * IMPORTANT caveat: hidden webview views can drop postMessage even with
 * retainContextWhenHidden — so we DO NOT push state into hidden views. State
 * lands frontend-side on resolve/visibility re-hydrate (backend session.current
 * seam), never via host->webview pushes while hidden.
 */
export class SurfaceViewProvider implements vscode.WebviewViewProvider {
  constructor(
    private readonly extensionUri: vscode.Uri,
    private readonly backend: Backend,
    private readonly out: vscode.OutputChannel,
    private readonly surface: Surface,
  ) {}

  resolveWebviewView(view: vscode.WebviewView): void {
    view.webview.options = {
      enableScripts: true,
      localResourceRoots: [vscode.Uri.joinPath(this.extensionUri, 'media')],
    };
    const mount = mountSpa(view.webview, this.extensionUri, this.backend, this.out, this.surface);
    view.onDidDispose(() => mount.dispose());
  }
}
