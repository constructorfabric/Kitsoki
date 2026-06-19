// ide-tools.ts — the editor-facing half of the IDE-MCP bridge.
//
// IdeTools maps the Claude Code IDE tool set (openFile / openDiff / the three
// read verbs) onto real VS Code APIs. The kitsoki backend dials the extension's
// MCP server (ide-server.ts) and issues `tools/call`; this is where those calls
// become showTextDocument / vscode.diff / TabGroups reads. The wire shapes
// mirror what a real editor returns over the Claude Code IDE MCP — the Go host
// handlers (internal/host/ide_handlers.go) normalise them, so fidelity here is
// load-bearing.
//
// openDiff is intentionally a thin stub in this slice (returns {ok:true}); the
// native diff + accept/reject verdict gate + inline comment land in
// DiffController (see ide-diff.ts) and replace this method. Everything else is
// the genuine, hand-drivable behaviour.

import * as vscode from 'vscode';

/** A line/character position as the IDE MCP encodes it. */
interface WirePosition {
  line: number;
  character: number;
}
interface WireRange {
  start: WirePosition;
  end: WirePosition;
}

function toWirePosition(p: vscode.Position): WirePosition {
  return { line: p.line, character: p.character };
}
function toWireRange(r: vscode.Range): WireRange {
  return { start: toWirePosition(r.start), end: toWirePosition(r.end) };
}

/** Coerce an inbound IDE-MCP range object into a vscode.Range (best-effort). */
function fromWireRange(raw: unknown): vscode.Range | undefined {
  const r = raw as Partial<WireRange> | undefined;
  if (!r || !r.start || !r.end) return undefined;
  return new vscode.Range(
    new vscode.Position(r.start.line ?? 0, r.start.character ?? 0),
    new vscode.Position(r.end.line ?? 0, r.end.character ?? 0),
  );
}

/**
 * IdeTools fulfils the MCP tool set against the live VS Code window. It is
 * injected into IdeServer as the tool dispatcher (DI seam) so the transport
 * stays editor-agnostic and the handlers stay socket-agnostic. Stateful editor
 * concerns (the diff verdict gate) get their own collaborators wired in later.
 */
export class IdeTools {
  constructor(private readonly out: vscode.OutputChannel) {}

  /**
   * Dispatch one `tools/call` by name. Returns the structured payload the
   * server JSON-encodes into the MCP result envelope. Throwing surfaces to the
   * caller as an `isError:true` envelope.
   */
  async dispatch(name: string, args: Record<string, unknown>): Promise<unknown> {
    switch (name) {
      case 'openFile':
        return this.openFile(args);
      case 'openDiff':
        return this.openDiff(args);
      case 'getDiagnostics':
        return this.getDiagnostics(args);
      case 'getCurrentSelection':
        return this.getCurrentSelection();
      case 'getOpenEditors':
        return this.getOpenEditors();
      default:
        throw new Error(`ide: unknown tool ${name}`);
    }
  }

  /**
   * openFile — open `path` as a normal editor tab (visible in the Explorer,
   * hand-editable). When the file is already open VS Code just focuses it, so
   * re-opening a growing brief across clarification rounds keeps the same tab.
   * A missing/unopenable file is a graceful {ok:false}, never a throw — the
   * story's `on_error: .` keeps the room.
   */
  private async openFile(args: Record<string, unknown>): Promise<{ ok: boolean }> {
    const p = typeof args.path === 'string' ? args.path : '';
    if (!p) return { ok: false };
    try {
      const doc = await vscode.workspace.openTextDocument(vscode.Uri.file(p));
      const editor = await vscode.window.showTextDocument(doc, { preview: false });
      const range = fromWireRange(args.range);
      if (range) {
        editor.selection = new vscode.Selection(range.start, range.end);
        editor.revealRange(range, vscode.TextEditorRevealType.InCenter);
      }
      this.out.appendLine(`[ide] openFile ${p}`);
      return { ok: true };
    } catch (e) {
      this.out.appendLine(`[ide] openFile failed for ${p}: ${(e as Error).message}`);
      return { ok: false };
    }
  }

  /**
   * openDiff — STUB for this slice: acknowledges the call so the bridge is
   * exercised end-to-end. The native side-by-side diff, inline comment, and the
   * accept/reject verdict gate replace this in DiffController.
   */
  private async openDiff(args: Record<string, unknown>): Promise<{ ok: boolean }> {
    const p = typeof args.path === 'string' ? args.path : '';
    this.out.appendLine(`[ide] openDiff ${p} (stub)`);
    return { ok: true };
  }

  /**
   * getDiagnostics — workspace diagnostics, optionally narrowed to `path`.
   * Shape mirrors the editor's getDiagnostics ({uri, diagnostics:[…]}).
   */
  private async getDiagnostics(args: Record<string, unknown>): Promise<unknown> {
    const p = typeof args.path === 'string' ? args.path : '';
    const encode = (uri: vscode.Uri, diags: readonly vscode.Diagnostic[]) => ({
      uri: uri.toString(),
      diagnostics: diags.map((d) => ({
        message: d.message,
        severity: severityName(d.severity),
        source: d.source ?? '',
        range: toWireRange(d.range),
      })),
    });
    if (p) {
      const uri = vscode.Uri.file(p);
      return encode(uri, vscode.languages.getDiagnostics(uri));
    }
    // No path: return the first file that has diagnostics (the common probe).
    for (const [uri, diags] of vscode.languages.getDiagnostics()) {
      if (diags.length) return encode(uri, diags);
    }
    return { uri: '', diagnostics: [] };
  }

  /**
   * getCurrentSelection — the active editor's live selection. Shape mirrors the
   * editor ({filePath, text, selection:{start,end}}); the Go handler normalises.
   */
  private async getCurrentSelection(): Promise<unknown> {
    const ed = vscode.window.activeTextEditor;
    if (!ed) return { filePath: '', text: '', selection: null };
    return {
      filePath: ed.document.uri.fsPath,
      text: ed.document.getText(ed.selection),
      selection: toWireRange(ed.selection),
    };
  }

  /**
   * getOpenEditors — the open tabs across groups. Shape mirrors VS Code's
   * TabGroups ({tabs:[{uri, fileName, isActive, …}]}).
   */
  private async getOpenEditors(): Promise<unknown> {
    const tabs: unknown[] = [];
    for (const group of vscode.window.tabGroups.all) {
      for (const tab of group.tabs) {
        const input = tab.input;
        if (input instanceof vscode.TabInputText) {
          tabs.push({
            uri: input.uri.toString(),
            fileName: input.uri.fsPath,
            isActive: tab.isActive,
            isGroupActive: group.isActive,
            isDirty: tab.isDirty,
            label: tab.label,
            languageId: '',
          });
        }
      }
    }
    return { tabs };
  }
}

function severityName(s: vscode.DiagnosticSeverity): string {
  switch (s) {
    case vscode.DiagnosticSeverity.Error:
      return 'error';
    case vscode.DiagnosticSeverity.Warning:
      return 'warning';
    case vscode.DiagnosticSeverity.Information:
      return 'information';
    case vscode.DiagnosticSeverity.Hint:
      return 'hint';
    default:
      return 'info';
  }
}
