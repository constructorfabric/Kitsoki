// ide-diff.ts — the native refine-diff with an accept/reject verdict gate.
//
// When the story refines the PRD/Design it invokes host.ide.open_diff, which the
// Go handler turns into an MCP openDiff tools/call and BLOCKS on the response
// (link.CallTool awaits indefinitely). DiffController fulfils that call by:
//   1. opening a real side-by-side diff — left = the on-disk file, right = the
//      proposed text (a virtual doc), so VS Code's stock diff navigation applies;
//   2. exposing Accept/Reject via two native affordances (editor title-bar
//      actions and a CodeLens at the top of the proposed doc) plus the command
//      palette — all firing the same commands;
//   3. NOT resolving the openDiff response until the operator decides. Accept
//      writes the proposed text to the file (the change is applied, native
//      intuition) and returns verdict:"accepted"; Reject leaves the file and
//      returns verdict:"rejected". The Go handler surfaces that verdict so the
//      story branches (publish vs re-refine).
//
// Closing the diff without deciding resolves as "rejected" so the suspended turn
// never hangs.

import * as vscode from 'vscode';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { resolveWorkspacePath, chatDocColumn } from './ide-tools';

export const DIFF_SCHEME = 'kitsoki-diff';
export type Verdict = 'accepted' | 'rejected';

interface Pending {
  id: number;
  filePath: string;
  newText: string;
  rightUri: vscode.Uri;
  resolve: (v: { ok: boolean; verdict: Verdict }) => void;
}

interface Logger {
  appendLine(line: string): void;
}

/**
 * DiffController owns the refine-diff lifecycle and the accept/reject verdict.
 * One diff is pending at a time (the story drives one refine per blocking turn);
 * a second open auto-rejects the first defensively.
 */
export class DiffController {
  private readonly contents = new Map<string, string>(); // virtual path -> proposed text
  private pending: Pending | undefined;
  private seq = 0;
  private readonly disposables: vscode.Disposable[] = [];

  constructor(private readonly out: Logger) {
    // Right-hand "proposed" document is served from memory (no temp files).
    this.disposables.push(
      vscode.workspace.registerTextDocumentContentProvider(DIFF_SCHEME, {
        provideTextDocumentContent: (uri) => this.contents.get(uri.path) ?? '',
      }),
    );

    // CodeLens at the top of the proposed doc — a second native affordance.
    this.disposables.push(
      vscode.languages.registerCodeLensProvider(
        { scheme: DIFF_SCHEME },
        {
          provideCodeLenses: () => {
            if (!this.pending) return [];
            const top = new vscode.Range(0, 0, 0, 0);
            return [
              new vscode.CodeLens(top, { title: '$(check) Accept', command: 'kitsoki.diff.accept' }),
              new vscode.CodeLens(top, { title: '$(x) Reject', command: 'kitsoki.diff.reject' }),
            ];
          },
        },
      ),
    );

    // The commands the three affordances + palette all fire.
    this.disposables.push(
      vscode.commands.registerCommand('kitsoki.diff.accept', () => this.resolve('accepted')),
      vscode.commands.registerCommand('kitsoki.diff.reject', () => this.resolve('rejected')),
    );

    // Closing the diff tab without deciding = reject (never hang the turn).
    this.disposables.push(
      vscode.window.tabGroups.onDidChangeTabs((e) => {
        if (!this.pending) return;
        const want = this.pending.rightUri.toString();
        for (const tab of e.closed) {
          const input = tab.input;
          if (input instanceof vscode.TabInputTextDiff && input.modified.toString() === want) {
            this.out.appendLine('[ide] diff closed without a decision -> reject');
            this.resolve('rejected');
          }
        }
      }),
    );
  }

  /**
   * Open the diff and block until the operator accepts/rejects. Returns the
   * verdict for the Go handler to surface. args: {path, new_text | new_text_path,
   * title?}. The proposed text is `new_text` inline, or read from
   * `new_text_path` (the staged draft on disk — avoids piping a large doc
   * through the MCP envelope).
   */
  async open(args: Record<string, unknown>): Promise<{ ok: boolean; verdict: Verdict }> {
    // Defensively clear any prior pending diff (one at a time).
    if (this.pending) this.resolve('rejected');

    const filePath = resolveWorkspacePath(typeof args.path === 'string' ? args.path : '');
    let newText = typeof args.new_text === 'string' ? args.new_text : '';
    if (!newText && typeof args.new_text_path === 'string' && args.new_text_path) {
      try {
        newText = fs.readFileSync(resolveWorkspacePath(args.new_text_path), 'utf8');
      } catch (e) {
        this.out.appendLine(`[ide] openDiff: could not read new_text_path: ${(e as Error).message}`);
      }
    }
    const title = typeof args.title === 'string' && args.title ? args.title : 'Kitsoki — proposed change';

    if (!filePath) {
      return { ok: false, verdict: 'rejected' };
    }

    const id = ++this.seq;
    const vpath = `/${id}/${path.basename(filePath) || 'proposed.md'}`;
    this.contents.set(vpath, newText);
    const rightUri = vscode.Uri.from({ scheme: DIFF_SCHEME, path: vpath });
    const leftUri = vscode.Uri.file(filePath);

    await vscode.commands.executeCommand(
      'vscode.diff',
      leftUri,
      rightUri,
      title,
      { viewColumn: chatDocColumn(), preview: false, preserveFocus: true } as vscode.TextDocumentShowOptions,
    );

    // Drive the editor/title `when` clause for the Accept/Reject toolbar actions.
    await vscode.commands.executeCommand('setContext', 'kitsoki.diffPending', true);
    this.out.appendLine(`[ide] openDiff ${filePath} (awaiting verdict)`);

    return new Promise((resolve) => {
      this.pending = { id, filePath, newText, rightUri, resolve };
    });
  }

  /** Resolve the pending diff: apply on accept, clean up, unblock the turn. */
  private resolve(verdict: Verdict): void {
    const p = this.pending;
    if (!p) return;
    this.pending = undefined;

    if (verdict === 'accepted') {
      try {
        fs.writeFileSync(p.filePath, p.newText);
        this.out.appendLine(`[ide] diff accepted -> wrote ${p.filePath}`);
      } catch (e) {
        this.out.appendLine(`[ide] diff accept write failed: ${(e as Error).message}`);
      }
    } else {
      this.out.appendLine(`[ide] diff rejected -> ${p.filePath} unchanged`);
    }

    this.contents.delete(p.rightUri.path);
    void vscode.commands.executeCommand('setContext', 'kitsoki.diffPending', false);
    this.closeDiffTab(p.rightUri);
    p.resolve({ ok: true, verdict });
  }

  /** Best-effort close of the diff tab so the editor returns to a clean state. */
  private closeDiffTab(rightUri: vscode.Uri): void {
    const want = rightUri.toString();
    for (const group of vscode.window.tabGroups.all) {
      for (const tab of group.tabs) {
        const input = tab.input;
        if (input instanceof vscode.TabInputTextDiff && input.modified.toString() === want) {
          void vscode.window.tabGroups.close(tab);
        }
      }
    }
  }

  dispose(): void {
    if (this.pending) this.resolve('rejected');
    for (const d of this.disposables) d.dispose();
    this.disposables.length = 0;
  }
}
