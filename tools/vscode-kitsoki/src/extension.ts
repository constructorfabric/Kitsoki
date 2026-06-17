// extension.ts — activation entry point. The primary surface is the editor-area
// KitsokiPanel (chat front/center + hint rail). The Activity Bar icon hosts a
// thin launcher view whose Welcome button — and first reveal — open that panel.

import * as vscode from 'vscode';
import * as fs from 'node:fs';
import { Backend } from './backend';
import {
  ChatPanel,
  SurfaceViewProvider,
  makeChatPanelSerializer,
  CHAT_PANEL_VIEW_TYPE,
} from './webview';

let backend: Backend | undefined;

/**
 * Tee an OutputChannel's lines to a file when `logPath` is set (the e2e gate
 * sets KITSOKI_E2E_LOG so the extension host's diagnostics — backend spawn,
 * health, relay errors — survive outside the in-editor Output panel). A no-op
 * passthrough otherwise; production behaviour is unchanged.
 */
function teeToFile(out: vscode.OutputChannel, logPath: string | undefined): vscode.OutputChannel {
  if (!logPath) return out;
  const write = (s: string) => {
    try {
      fs.appendFileSync(logPath, s);
    } catch {
      /* best-effort diagnostic */
    }
  };
  return new Proxy(out, {
    get(target, prop, recv) {
      if (prop === 'appendLine') return (line: string) => { write(line + '\n'); target.appendLine(line); };
      if (prop === 'append') return (value: string) => { write(value); target.append(value); };
      return Reflect.get(target, prop, recv);
    },
  });
}

/**
 * The Activity Bar launcher view. It renders no tree rows — its `viewsWelcome`
 * (package.json) shows the "Open Kitsoki Chat" button — and auto-opens the editor
 * panel the first time the user reveals it (clicks the Kitsoki Activity Bar icon).
 * Least-surprise: revealing Kitsoki opens Kitsoki.
 */
class LaunchViewProvider implements vscode.TreeDataProvider<never> {
  getChildren(): never[] {
    return [];
  }
  getTreeItem(): vscode.TreeItem {
    return new vscode.TreeItem('');
  }
}

export function activate(context: vscode.ExtensionContext): void {
  const out = teeToFile(vscode.window.createOutputChannel('Kitsoki'), process.env.KITSOKI_E2E_LOG);
  context.subscriptions.push(out);

  const cwd = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;
  backend = new Backend(out, cwd);
  context.subscriptions.push({ dispose: () => backend?.dispose() });

  const openChat = () => ChatPanel.reveal(context.extensionUri, backend!, out);

  // Sidebar surfaces (trace / graph) live as webview views in the Kitsoki
  // container. Each builds its own Relay via mountSpa against the ONE shared
  // backend; the SPA re-hydrates frontend-side on resolve/visibility.
  context.subscriptions.push(
    vscode.window.registerWebviewViewProvider(
      'kitsoki.chat',
      new SurfaceViewProvider(context.extensionUri, backend, out, 'chat'),
    ),
    vscode.window.registerWebviewViewProvider(
      'kitsoki.trace',
      new SurfaceViewProvider(context.extensionUri, backend, out, 'trace'),
    ),
    vscode.window.registerWebviewViewProvider(
      'kitsoki.graph',
      new SurfaceViewProvider(context.extensionUri, backend, out, 'graph'),
    ),
  );

  // Revive the chat editor panel after reload/restart/window-move.
  context.subscriptions.push(
    vscode.window.registerWebviewPanelSerializer(
      CHAT_PANEL_VIEW_TYPE,
      makeChatPanelSerializer(context.extensionUri, backend, out),
    ),
  );

  // Launcher view in the Kitsoki Activity Bar container. createTreeView (not
  // registerTreeDataProvider) so we get onDidChangeVisibility to auto-open.
  const launchView = vscode.window.createTreeView('kitsoki.launch', {
    treeDataProvider: new LaunchViewProvider(),
  });
  let autoOpened = false;
  launchView.onDidChangeVisibility((e) => {
    if (e.visible && !autoOpened) {
      autoOpened = true;
      openChat();
    }
  });
  context.subscriptions.push(launchView);

  context.subscriptions.push(
    vscode.commands.registerCommand('kitsoki.openChat', openChat),
    // Pop-out button on the narrow sidebar Chat surface: promote the same chat to
    // the full editor-area panel (the richer embed layout). They share the one
    // backend session, so the conversation continues uninterrupted; the editor
    // panel becomes the focused surface.
    vscode.commands.registerCommand('kitsoki.popOutChat', openChat),
    vscode.commands.registerCommand('kitsoki.openTrace', () =>
      vscode.commands.executeCommand('kitsoki.trace.focus'),
    ),
    vscode.commands.registerCommand('kitsoki.openGraph', () =>
      vscode.commands.executeCommand('kitsoki.graph.focus'),
    ),
    vscode.commands.registerCommand('kitsoki.restartBackend', async () => {
      out.appendLine('[extension] restart backend requested');
      try {
        const base = await backend?.restart();
        void vscode.window.showInformationMessage(`Kitsoki backend restarted at ${base}`);
      } catch (e) {
        void vscode.window.showErrorMessage(`Kitsoki backend restart failed: ${(e as Error).message}`);
      }
    }),
  );
}

export function deactivate(): void {
  backend?.dispose();
  backend = undefined;
}
