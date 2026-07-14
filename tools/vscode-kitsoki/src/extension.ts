// extension.ts — activation entry point. Chat is a bottom-panel webview beside
// VS Code's Terminal / Ports / Playwright views. Trace and Graph remain dockable
// webview views in the Kitsoki Surfaces activity-bar container. Chat deliberately
// does not use an editor-area WebviewPanel, so opening it never consumes an editor
// tab or displaces the file the operator is working on.
//
// `Kitsoki: Open Chat` goes straight to a story PICKER: it lists the backend's
// discovered stories, starts the chosen one (session.new), then reveals the
// bottom panel — which immediately reflects the new session via the backend's
// current-session seam (their subscribeCurrentSession / getCurrentSession on
// mount).

import * as vscode from 'vscode';
import * as fs from 'node:fs';
import { Backend, type StoryHeader } from './backend';
import { IdeServer } from './ide-server';
import { IdeTools } from './ide-tools';
import { DiffController } from './ide-diff';
import { SurfaceViewProvider } from './webview';

let backend: Backend | undefined;
let ideServer: IdeServer | undefined;

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

export function activate(context: vscode.ExtensionContext): void {
  const out = teeToFile(vscode.window.createOutputChannel('Kitsoki'), process.env.KITSOKI_E2E_LOG);
  context.subscriptions.push(out);

  const cwd = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath;

  // Stand up the IDE-MCP server FIRST so its port is ready before the backend
  // spawns: the backend reads CLAUDE_CODE_SSE_PORT to dial back into this window
  // for host.ide.* (open the brief/PRD, show refine diffs). Tools are injected
  // (IdeTools) so the server is pure transport.
  const diff = new DiffController(out);
  context.subscriptions.push(diff);
  ideServer = new IdeServer(out, new IdeTools(out, diff), {
    workspaceFolders: (vscode.workspace.workspaceFolders ?? []).map((f) => f.uri.fsPath),
  });
  context.subscriptions.push({ dispose: () => ideServer?.dispose() });

  backend = new Backend(out, cwd, () => ideServer!.ready);
  context.subscriptions.push({ dispose: () => backend?.dispose() });

  // Open Chat -> story picker -> start session -> reveal the bottom Chat panel.
  // The surfaces don't need to be told which session: starting it makes it the
  // backend's current session, and each surface adopts that (seed-on-subscribe +
  // getCurrentSession on mount). So we just start it and focus the panel Chat.
  const openChat = async () => {
    let stories: StoryHeader[];
    try {
      stories = await backend!.rpc<StoryHeader[]>('runstatus.stories.list', {});
    } catch (e) {
      void vscode.window.showErrorMessage(`Kitsoki: could not list stories — ${(e as Error).message}`);
      return;
    }
    if (!stories.length) {
      void vscode.window.showWarningMessage(
        'Kitsoki: no stories found. Set the kitsoki.storiesDir (or kitsoki.flow) setting and retry.',
      );
      return;
    }

    // Default the picker to the kitsoki-dev dogfood story: discovery orders
    // stories lexicographically (so 'bugfix' would otherwise sit first and be the
    // highlighted/Enter default), but kitsoki-dev is the one we almost always want
    // in the kitsoki repo. Float it to the top; the rest keep their order.
    const ordered = [...stories].sort((a, b) => {
      const ak = a.app_id === 'kitsoki-dev' ? 0 : 1;
      const bk = b.app_id === 'kitsoki-dev' ? 0 : 1;
      return ak - bk;
    });
    const picks = ordered.map((s) => ({
      label: s.title || s.app_id || s.path,
      description: s.active_sessions.length ? `${s.active_sessions.length} active` : '',
      detail: s.path,
      story: s,
    }));
    const chosen = await vscode.window.showQuickPick(picks, {
      title: 'Kitsoki: Start a Story',
      placeHolder: 'Pick a story to start a chat session',
      matchOnDetail: true,
    });
    if (!chosen) return; // user cancelled the picker

    try {
      await backend!.rpc('runstatus.session.new', { story_path: chosen.story.path });
    } catch (e) {
      void vscode.window.showErrorMessage(
        `Kitsoki: failed to start "${chosen.label}" — ${(e as Error).message}`,
      );
      return;
    }

    // Reveal the bottom panel so it immediately reflects the new session.
    await vscode.commands.executeCommand('kitsoki.chat.focus');
  };

  // Chat is contributed to the bottom panel; trace / graph live in the Kitsoki
  // Surfaces activity-bar container. Each builds its own Relay via mountSpa
  // against the ONE shared backend; the SPA re-hydrates on resolve/visibility.
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

  context.subscriptions.push(
    vscode.commands.registerCommand('kitsoki.openChat', openChat),
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
  ideServer?.dispose();
  ideServer = undefined;
}
