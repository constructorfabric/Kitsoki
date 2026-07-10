// backend.ts — owns the spawned `kitsoki web` child process.
//
// The kitsoki `web` command prints the requested addr, not the resolved port,
// so `--addr 127.0.0.1:0` is unusable. Instead we allocate a free port in Node
// (net.createServer().listen(0)), spawn the binary bound to it, and health-poll
// `GET /` until it answers before reporting ready. No backend change required.

import * as vscode from 'vscode';
import { spawn, type ChildProcess } from 'node:child_process';
import * as net from 'node:net';
import { resolveBinary, binaryEnv, spawnErrorHint, resolveStoriesDir } from './backend-resolve';
export { resolveBinary, binaryEnv, spawnErrorHint, resolveStoriesDir } from './backend-resolve';

export interface BackendConfig {
  binaryPath: string; // "" => "kitsoki" on PATH
  flow: string;
  hostCassette: string;
  storiesDir: string;
  mode: string; // "" => backend default (staged); "one-shot" auto-advances synthetic gates
  ticketRepo: string; // "" => local issues/bugs/*.md filing; owner/repo => GitHub issues (needs GH_TOKEN)
}

/**
 * A discovered story, as returned by `runstatus.stories.list`. `path` is the
 * ABSOLUTE app.yaml path — the canonical key passed back to `session.new`.
 * `title`/`app_id` are display-only; `active_sessions` lists live session ids
 * already started from this story.
 */
export interface StoryHeader {
  path: string;
  app_id: string;
  title: string;
  active_sessions: string[];
}

/** Read the extension settings into a BackendConfig. */
export function readConfig(): BackendConfig {
  const cfg = vscode.workspace.getConfiguration('kitsoki');
  return {
    binaryPath: (cfg.get<string>('binaryPath') ?? '').trim(),
    flow: (cfg.get<string>('flow') ?? '').trim(),
    hostCassette: (cfg.get<string>('hostCassette') ?? '').trim(),
    storiesDir: (cfg.get<string>('storiesDir') ?? '').trim(),
    mode: (cfg.get<string>('mode') ?? '').trim(),
    ticketRepo: (cfg.get<string>('ticketRepo') ?? '').trim(),
  };
}

/** Allocate a free localhost TCP port by binding to :0 and reading it back. */
export function allocatePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.once('error', reject);
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address();
      if (addr && typeof addr === 'object') {
        const port = addr.port;
        srv.close(() => resolve(port));
      } else {
        srv.close(() => reject(new Error('could not resolve allocated port')));
      }
    });
  });
}

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

/**
 * Manages the lifecycle of one `kitsoki web` backend. `start()` is idempotent
 * across restarts; `dispose()` kills the child. The OutputChannel receives the
 * child's stdout/stderr.
 */
export class Backend {
  private child: ChildProcess | undefined;
  private _base = '';
  private starting: Promise<string> | undefined;

  // Fires with the NEW base URL each time the backend is restarted onto a fresh
  // port. Mounted webviews subscribe so they can re-point their relay and reboot
  // the SPA — otherwise they keep talking to the dead old port ("fetch failed").
  private readonly _onDidRestart = new vscode.EventEmitter<string>();
  readonly onDidRestart = this._onDidRestart.event;

  constructor(
    private readonly out: vscode.OutputChannel,
    private readonly cwd: string | undefined,
    // Resolves to the extension's IDE-MCP server port (or undefined when none).
    // Passed to the spawned backend as CLAUDE_CODE_SSE_PORT so its host.ide.*
    // verbs connect back to THIS VS Code window. Optional: omit for no IDE link.
    private readonly idePortReady?: () => Promise<number | undefined>,
  ) {}

  /** The backend base URL once started, e.g. "http://127.0.0.1:54231". */
  get base(): string {
    return this._base;
  }

  get running(): boolean {
    return !!this.child && this.child.exitCode === null;
  }

  /** Start (or return the in-flight start of) the backend. Resolves to the base URL. */
  start(): Promise<string> {
    if (this.starting) return this.starting;
    this.starting = this.doStart().catch((e) => {
      this.starting = undefined;
      throw e;
    });
    return this.starting;
  }

  /** Kill the current child and start a fresh one (on a new port). */
  async restart(): Promise<string> {
    this.stop();
    this.starting = undefined;
    const base = await this.start();
    // Let mounted webviews re-point their relay + reboot the SPA at the new port.
    this._onDidRestart.fire(base);
    return base;
  }

  /**
   * Issue a JSON-RPC call against the backend's `POST /rpc`, starting it first
   * if needed. This is the SAME control plane the webview SPA drives through the
   * relay — the extension host can speak it directly (e.g. to list / start
   * stories from a command) without routing through a webview. Throws on a
   * non-2xx response or a JSON-RPC error.
   */
  async rpc<T = unknown>(method: string, params: Record<string, unknown> = {}): Promise<T> {
    const base = await this.start();
    const res = await fetch(`${base}/rpc`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const json = (await res.json()) as { result?: T; error?: { message: string } };
    if (json.error) throw new Error(json.error.message);
    return json.result as T;
  }

  private async doStart(): Promise<string> {
    const cfg = readConfig();
    const bin = resolveBinary(cfg.binaryPath, this.cwd);
    const port = await allocatePort();
    const args = ['web', '--addr', `127.0.0.1:${port}`];
    if (cfg.flow) args.push('--flow', cfg.flow);
    if (cfg.hostCassette) args.push('--host-cassette', cfg.hostCassette);
    const storiesDir = resolveStoriesDir(cfg.storiesDir, this.cwd);
    if (storiesDir) {
      if (!cfg.storiesDir) this.out.appendLine(`[backend] auto-discovered stories dir: ${storiesDir}`);
      args.push('--stories-dir', storiesDir);
    }
    if (cfg.mode) args.push('--mode', cfg.mode);
    // `kitsoki web`'s own --ticket-repo default is "constructorfabric/Kitsoki"
    // (kitsoki's OWN dogfood repo — see cmd/kitsoki/web.go) — wrong for an
    // arbitrary project opened in this extension, and it silently requires
    // GH_TOKEN/GITHUB_TOKEN the operator likely hasn't set. Always pass the
    // flag explicitly so the extension's own default is LOCAL filing
    // (issues/bugs/*.md under the workspace) unless the operator opts into a
    // real repo via kitsoki.ticketRepo.
    args.push('--ticket-repo', cfg.ticketRepo);

    // When the extension's IDE-MCP server is up, seed CLAUDE_CODE_SSE_PORT so the
    // backend's IDE link discovers our lock and dials this window outright (the
    // integrated-terminal fast path). Absent it, the backend runs with no link
    // and host.ide.* return connected:false — the graceful no-IDE posture.
    const env: NodeJS.ProcessEnv = binaryEnv(process.env);
    const idePort = this.idePortReady ? await this.idePortReady() : undefined;
    if (idePort) {
      env.CLAUDE_CODE_SSE_PORT = String(idePort);
      this.out.appendLine(`[backend] IDE link enabled (CLAUDE_CODE_SSE_PORT=${idePort})`);
    }

    this.out.appendLine(`[backend] spawn: ${bin} ${args.join(' ')}`);
    const child = spawn(bin, args, {
      cwd: this.cwd,
      env,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    this.child = child;

    child.stdout?.on('data', (d: Buffer) => this.out.append(d.toString()));
    child.stderr?.on('data', (d: Buffer) => this.out.append(d.toString()));
    child.on('exit', (code, signal) => {
      this.out.appendLine(`[backend] exited code=${code} signal=${signal}`);
      if (this.child === child) {
        this.child = undefined;
        this._base = '';
        this.starting = undefined;
      }
    });

    // A spawn failure (most commonly ENOENT — the binary isn't on the spawner's
    // PATH) fires 'error', NOT 'exit', so the health poll would otherwise spin
    // for its full 30s and surface the opaque Node "fetch failed". Race a
    // promise that rejects on 'error' with an actionable message instead.
    const spawnFailed = new Promise<never>((_, reject) => {
      child.once('error', (err) => {
        const hint = spawnErrorHint(bin, err as NodeJS.ErrnoException);
        this.out.appendLine(`[backend] spawn error: ${err.message}${hint}`);
        void vscode.window.showErrorMessage(`Kitsoki: could not launch the backend${hint || `: ${err.message}`}`);
        reject(new Error(`could not launch kitsoki backend: ${err.message}${hint}`));
      });
    });

    const base = `http://127.0.0.1:${port}`;
    await Promise.race([this.healthPoll(base, child), spawnFailed]);
    this._base = base;
    this.out.appendLine(`[backend] healthy at ${base}`);
    return base;
  }

  private async healthPoll(base: string, child: ChildProcess): Promise<void> {
    const deadline = Date.now() + 30_000;
    let lastErr = 'no response';
    while (Date.now() < deadline) {
      if (child.exitCode !== null) {
        throw new Error(`backend exited (code ${child.exitCode}) before becoming healthy`);
      }
      try {
        const res = await fetch(`${base}/`, { method: 'GET' });
        if (res.ok) return;
        lastErr = `HTTP ${res.status}`;
      } catch (e) {
        lastErr = (e as Error).message;
      }
      await sleep(250);
    }
    throw new Error(`backend health poll timed out: ${lastErr}`);
  }

  private stop(): void {
    if (this.child && this.child.exitCode === null) {
      this.child.kill();
    }
    this.child = undefined;
    this._base = '';
  }

  dispose(): void {
    this.stop();
    this._onDidRestart.dispose();
  }
}
