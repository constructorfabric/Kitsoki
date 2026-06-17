// backend.ts — owns the spawned `kitsoki web` child process.
//
// The kitsoki `web` command prints the requested addr, not the resolved port,
// so `--addr 127.0.0.1:0` is unusable. Instead we allocate a free port in Node
// (net.createServer().listen(0)), spawn the binary bound to it, and health-poll
// `GET /` until it answers before reporting ready. No backend change required.

import * as vscode from 'vscode';
import { spawn, type ChildProcess } from 'node:child_process';
import * as net from 'node:net';

export interface BackendConfig {
  binaryPath: string; // "" => "kitsoki" on PATH
  flow: string;
  hostCassette: string;
  storiesDir: string;
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

  constructor(
    private readonly out: vscode.OutputChannel,
    private readonly cwd: string | undefined,
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

  /** Kill the current child and start a fresh one. */
  async restart(): Promise<string> {
    this.stop();
    this.starting = undefined;
    return this.start();
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
    const bin = cfg.binaryPath || 'kitsoki';
    const port = await allocatePort();
    const args = ['web', '--addr', `127.0.0.1:${port}`];
    if (cfg.flow) args.push('--flow', cfg.flow);
    if (cfg.hostCassette) args.push('--host-cassette', cfg.hostCassette);
    if (cfg.storiesDir) args.push('--stories-dir', cfg.storiesDir);

    this.out.appendLine(`[backend] spawn: ${bin} ${args.join(' ')}`);
    const child = spawn(bin, args, {
      cwd: this.cwd,
      env: process.env,
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
    child.on('error', (err) => {
      this.out.appendLine(`[backend] spawn error: ${err.message}`);
    });

    const base = `http://127.0.0.1:${port}`;
    await this.healthPoll(base, child);
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
  }
}
