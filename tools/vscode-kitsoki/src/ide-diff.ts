// ide-diff.ts — the native refine-diff with an accept/reject verdict gate.
//
// DiffController fulfils host.ide.open_diff (via ide-tools.ts's openDiff) in
// two shapes, both landing on ONE collective accept/reject verdict:
//
//  Mode B — {path, new_text | new_text_path, title?}: review PROPOSED content
//    not yet applied. Left = the on-disk file, right = the proposed text (a
//    virtual doc). Accept WRITES the proposed text to the file (the change is
//    applied, native intuition); reject leaves the file untouched.
//
//  Mode A — {paths: [...], base, title?}: review ALREADY-APPLIED
//    working-tree edits against a git base ref ("working-tree-vs-base"), the
//    shape stories/bugfix's reviewing_external sends. Left = each file's
//    content at `base` (via git show — see diff-mode-a.ts), right = the real
//    on-disk file. Nothing is written back on accept — the edits are already
//    on disk; accept/reject is purely the operator's verdict on what's
//    already there. Multiple files open as separate diff tabs so the operator
//    can page through every one, but they share ONE pending verdict — the
//    first Accept/Reject decides the whole set (reviewing_external expects a
//    single verdict for the batch, not a per-file one).
//
// Both modes expose Accept/Reject via the same native affordances (editor
// title-bar actions and a CodeLens at the top of each diff's virtual/base
// doc) plus the command palette — all firing the same commands. Closing any
// one of the diff tabs without deciding resolves the whole set as "rejected"
// so the suspended turn never hangs.

import * as vscode from 'vscode';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { resolveWorkspacePath, chatDocColumn } from './ide-tools';
import { normalizeModeAArgs, loadBaseContent, realGitShow, type GitShow } from './diff-mode-a';

export const DIFF_SCHEME = 'kitsoki-diff';
export type Verdict = 'accepted' | 'rejected';

/** One opened diff tab's identity, so resolve()/cleanup can address it. */
interface DiffPair {
  left: vscode.Uri;
  right: vscode.Uri;
}

interface Pending {
  id: number;
  mode: 'A' | 'B';
  diffs: DiffPair[];
  // Mode B only — the write-back target on accept.
  filePath?: string;
  newText?: string;
  resolve: (v: { ok: boolean; verdict: Verdict }) => void;
}

interface Logger {
  appendLine(line: string): void;
}

/**
 * DiffController owns the refine-diff lifecycle and the accept/reject verdict.
 * One diff (Mode A or Mode B, possibly spanning several files) is pending at
 * a time (the story drives one review per blocking turn); a second open
 * auto-rejects the first defensively.
 */
export class DiffController {
  private readonly contents = new Map<string, string>(); // virtual path -> content
  private pending: Pending | undefined;
  private seq = 0;
  private readonly disposables: vscode.Disposable[] = [];

  constructor(
    private readonly out: Logger,
    // Mode A's git-show seam — injectable so tests never spawn a real git
    // process; defaults to the real `git show <ref>:<path>` in production.
    private readonly gitShow: GitShow = realGitShow,
  ) {
    // Virtual doc provider — serves Mode B's proposed text AND Mode A's
    // base-ref content, keyed by the same DIFF_SCHEME virtual path.
    this.disposables.push(
      vscode.workspace.registerTextDocumentContentProvider(DIFF_SCHEME, {
        provideTextDocumentContent: (uri) => this.contents.get(uri.path) ?? '',
      }),
    );

    // CodeLens at the top of every open virtual (base/proposed) doc — a
    // second native affordance, shared by both modes.
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

    // Closing ANY of the pending diff tabs without deciding = reject the
    // whole set (never hang the turn).
    this.disposables.push(
      vscode.window.tabGroups.onDidChangeTabs((e) => {
        if (!this.pending) return;
        const want = new Set(this.pending.diffs.map((d) => d.right.toString()));
        for (const tab of e.closed) {
          const input = tab.input;
          if (input instanceof vscode.TabInputTextDiff && want.has(input.modified.toString())) {
            this.out.appendLine('[ide] diff closed without a decision -> reject');
            this.resolve('rejected');
            break;
          }
        }
      }),
    );
  }

  /**
   * Open the diff (Mode A or Mode B, dispatched by arg shape) and block until
   * the operator accepts/rejects. Returns the collective verdict for the Go
   * handler to surface.
   */
  async open(args: Record<string, unknown>): Promise<{ ok: boolean; verdict: Verdict }> {
    // Defensively clear any prior pending diff (one set at a time).
    if (this.pending) this.resolve('rejected');

    const modeA = normalizeModeAArgs(args);
    if (modeA) return this.openModeA(modeA);
    return this.openModeB(args);
  }

  /**
   * Mode A — {paths, base}: open one diff tab per file, left = the file's
   * content at `base` (git show), right = the real on-disk working-tree
   * file. Nothing is applied on accept; the edits are already on disk.
   */
  private async openModeA(modeA: { paths: string[]; base: string; title: string }): Promise<{ ok: boolean; verdict: Verdict }> {
    const workspaceRoot = vscode.workspace.workspaceFolders?.[0]?.uri.fsPath ?? '';
    const id = ++this.seq;
    const diffs: DiffPair[] = [];

    for (const relPath of modeA.paths) {
      const absPath = resolveWorkspacePath(relPath);
      let baseContent: string;
      try {
        baseContent = await loadBaseContent(this.gitShow, workspaceRoot, modeA.base, relPath);
      } catch (e) {
        this.out.appendLine(`[ide] openDiff (mode A): git show failed for ${relPath}: ${(e as Error).message}`);
        baseContent = '';
      }
      const vpath = `/${id}/base/${relPath.split('\\').join('/')}`;
      this.contents.set(vpath, baseContent);
      const leftUri = vscode.Uri.from({ scheme: DIFF_SCHEME, path: vpath });
      // The working-tree file may itself be gone (deleted since base) —
      // vscode.diff renders that side as a nonexistent-file placeholder,
      // which is the honest rendering of a deletion; no special-casing needed.
      const rightUri = vscode.Uri.file(absPath);
      diffs.push({ left: leftUri, right: rightUri });

      await vscode.commands.executeCommand(
        'vscode.diff',
        leftUri,
        rightUri,
        `${modeA.title} — ${relPath}`,
        { viewColumn: chatDocColumn(), preview: false, preserveFocus: true } as vscode.TextDocumentShowOptions,
      );
    }

    await vscode.commands.executeCommand('setContext', 'kitsoki.diffPending', true);
    this.out.appendLine(
      `[ide] openDiff (mode A) base=${modeA.base} paths=${JSON.stringify(modeA.paths)} (awaiting collective verdict)`,
    );

    return new Promise((resolve) => {
      this.pending = { id, mode: 'A', diffs, resolve };
    });
  }

  /**
   * Mode B — {path, new_text | new_text_path, title?}: proposed content not
   * yet on disk. `new_text` inline, or read from `new_text_path` (the staged
   * draft on disk — avoids piping a large doc through the MCP envelope).
   */
  private async openModeB(args: Record<string, unknown>): Promise<{ ok: boolean; verdict: Verdict }> {
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
      this.pending = { id, mode: 'B', diffs: [{ left: leftUri, right: rightUri }], filePath, newText, resolve };
    });
  }

  /** Resolve the pending diff(s): apply on accept (Mode B only), clean up, unblock the turn. */
  private resolve(verdict: Verdict): void {
    const p = this.pending;
    if (!p) return;
    this.pending = undefined;

    if (p.mode === 'B') {
      if (verdict === 'accepted') {
        try {
          fs.writeFileSync(p.filePath!, p.newText ?? '');
          this.out.appendLine(`[ide] diff accepted -> wrote ${p.filePath}`);
        } catch (e) {
          this.out.appendLine(`[ide] diff accept write failed: ${(e as Error).message}`);
        }
      } else {
        this.out.appendLine(`[ide] diff rejected -> ${p.filePath} unchanged`);
      }
    } else {
      // Mode A reviews already-applied edits — nothing to write back either
      // way, the verdict is purely the operator's judgment on what's on disk.
      this.out.appendLine(`[ide] mode A diff ${verdict} for ${p.diffs.length} file(s) (no write-back)`);
    }

    for (const d of p.diffs) {
      if (d.left.scheme === DIFF_SCHEME) this.contents.delete(d.left.path);
      if (d.right.scheme === DIFF_SCHEME) this.contents.delete(d.right.path);
    }
    void vscode.commands.executeCommand('setContext', 'kitsoki.diffPending', false);
    for (const d of p.diffs) this.closeDiffTab(d.right);
    p.resolve({ ok: true, verdict });
  }

  /** Best-effort close of the diff tab(s) so the editor returns to a clean state. */
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
