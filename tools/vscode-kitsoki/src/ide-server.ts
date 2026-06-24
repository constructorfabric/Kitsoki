// ide-server.ts — the extension acts as the Claude Code IDE MCP *server*.
//
// kitsoki's backend is the MCP *client*: it discovers a `~/.claude/ide/<port>.lock`
// file, dials ws://127.0.0.1:<port> with the lock's authToken, runs the
// initialize / notifications/initialized / tools/list handshake, then issues
// `tools/call`. This server stands that contract up inside the extension host so
// `host.ide.open_file` / `host.ide.open_diff` actually drive THIS VS Code window.
//
// Wire contract mirrors internal/ide (client.go / discovery.go / stubserver_test.go):
//   - lock JSON: {pid, workspaceFolders, ideName, transport:"ws", authToken}
//   - auth header: x-claude-code-ide-authorization (mismatch => reject upgrade)
//   - protocolVersion: 2024-11-05
//   - tools/call result envelope: {content:[{type:"text",text:<json>}], isError}
//
// Tool dispatch is injected (IdeTools) so this stays pure transport.

import * as http from 'node:http';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import * as crypto from 'node:crypto';
import type { Duplex } from 'node:stream';
import { WebSocketServer, type WebSocket } from 'ws';

const AUTH_HEADER = 'x-claude-code-ide-authorization';
const PROTOCOL_VERSION = '2024-11-05';

/** Minimal sink for diagnostics (a vscode.OutputChannel satisfies it). */
export interface Logger {
  appendLine(line: string): void;
}

const TOOL_NAMES = [
  'getDiagnostics',
  'getCurrentSelection',
  'getOpenEditors',
  'openFile',
  'openDiff',
] as const;

/** A handler that fulfils one `tools/call` by name and returns its payload. */
export interface ToolDispatcher {
  dispatch(name: string, args: Record<string, unknown>): Promise<unknown>;
}

/** A minimal JSON-RPC 2.0 frame as it arrives on the socket. */
interface RpcFrame {
  jsonrpc?: string;
  id?: number | string | null;
  method?: string;
  params?: { name?: string; arguments?: Record<string, unknown> };
}

/**
 * IdeServer owns the WebSocket MCP server + its lock file. `start()` binds a
 * loopback port, writes the lock, and resolves the port; `dispose()` closes the
 * server and removes the lock. Best-effort throughout: a bind/lock failure
 * resolves `ready` to undefined so the backend simply runs without an IDE
 * (host.ide.* return connected:false), never blocking activation.
 */
export class IdeServer {
  private server: http.Server | undefined;
  private wss: WebSocketServer | undefined;
  private readonly sockets = new Set<WebSocket>();
  private lockPath = '';
  private readonly authToken = crypto.randomUUID();
  private _port: number | undefined;

  /** Resolves to the bound port (or undefined if the server failed to start). */
  readonly ready: Promise<number | undefined>;

  // lockDir is where the <port>.lock is written; defaults to ~/.claude/ide (the
  // directory the kitsoki backend's discovery scans). Overridable for tests so
  // they never touch the real home.
  private readonly lockDir: string;
  // Absolute workspace roots advertised in the lock (for the backend's
  // workspace-prefix discovery). Injected by the caller from vscode.
  private readonly workspaceFolders: string[];

  constructor(
    private readonly out: Logger,
    private readonly tools: ToolDispatcher,
    opts?: { lockDir?: string; workspaceFolders?: string[] },
  ) {
    this.lockDir = opts?.lockDir ?? path.join(os.homedir(), '.claude', 'ide');
    this.workspaceFolders = opts?.workspaceFolders ?? [];
    this.ready = this.start().catch((e) => {
      this.out.appendLine(`[ide-server] start failed: ${(e as Error).message}`);
      return undefined;
    });
  }

  get port(): number | undefined {
    return this._port;
  }

  private async start(): Promise<number | undefined> {
    const server = http.createServer((_req, res) => {
      // Plain HTTP gets a terse hint; the real traffic is the ws upgrade.
      res.writeHead(426, { 'content-type': 'text/plain' });
      res.end('kitsoki ide-server: websocket only');
    });
    this.server = server;

    const wss = new WebSocketServer({ noServer: true });
    this.wss = wss;

    server.on('upgrade', (req, socket: Duplex, head) => {
      const token = req.headers[AUTH_HEADER];
      const got = Array.isArray(token) ? token[0] : token;
      if (got !== this.authToken) {
        // Reject before upgrading: the client surfaces this as a dial error.
        socket.write('HTTP/1.1 401 Unauthorized\r\n\r\n');
        socket.destroy();
        return;
      }
      wss.handleUpgrade(req, socket, head, (ws) => this.onConnection(ws));
    });

    const port = await new Promise<number>((resolve, reject) => {
      server.once('error', reject);
      server.listen(0, '127.0.0.1', () => {
        const addr = server.address();
        if (addr && typeof addr === 'object') resolve(addr.port);
        else reject(new Error('ide-server: could not resolve port'));
      });
    });
    this._port = port;
    this.writeLock(port);
    this.out.appendLine(`[ide-server] listening on 127.0.0.1:${port} (lock ${this.lockPath})`);
    return port;
  }

  private onConnection(ws: WebSocket): void {
    this.sockets.add(ws);
    ws.on('close', () => this.sockets.delete(ws));
    ws.on('error', () => this.sockets.delete(ws));
    ws.on('message', (data) => {
      let frame: RpcFrame;
      try {
        frame = JSON.parse(data.toString()) as RpcFrame;
      } catch {
        return; // ignore unparseable frames
      }
      // A notification (method, no id) — e.g. notifications/initialized — gets
      // no reply.
      if (frame.id === undefined || frame.id === null || !frame.method) return;
      void this.handleRequest(ws, frame);
    });
  }

  private async handleRequest(ws: WebSocket, frame: RpcFrame): Promise<void> {
    const id = frame.id;
    const reply = (result: unknown) =>
      this.send(ws, { jsonrpc: '2.0', id, result });
    const replyError = (code: number, message: string) =>
      this.send(ws, { jsonrpc: '2.0', id, error: { code, message } });

    switch (frame.method) {
      case 'initialize':
        reply({
          protocolVersion: PROTOCOL_VERSION,
          capabilities: { tools: {} },
          serverInfo: { name: 'kitsoki-vscode', version: '0.1.0' },
        });
        return;
      case 'tools/list':
        reply({ tools: TOOL_NAMES.map((n) => toolSpec(n)) });
        return;
      case 'tools/call': {
        const name = frame.params?.name ?? '';
        const args = frame.params?.arguments ?? {};
        try {
          const payload = await this.tools.dispatch(name, args);
          reply(envelope(payload, false));
        } catch (e) {
          reply(envelope((e as Error).message, true));
        }
        return;
      }
      default:
        replyError(-32601, 'method not found');
    }
  }

  private send(ws: WebSocket, msg: unknown): void {
    try {
      ws.send(JSON.stringify(msg));
    } catch (e) {
      this.out.appendLine(`[ide-server] send failed: ${(e as Error).message}`);
    }
  }

  private writeLock(port: number): void {
    const dir = this.lockDir;
    fs.mkdirSync(dir, { recursive: true });
    const body = JSON.stringify({
      pid: process.pid,
      workspaceFolders: this.workspaceFolders,
      ideName: 'Visual Studio Code',
      transport: 'ws',
      runningInWindows: process.platform === 'win32',
      authToken: this.authToken,
    });
    this.lockPath = path.join(dir, `${port}.lock`);
    fs.writeFileSync(this.lockPath, body, { mode: 0o600 });
  }

  dispose(): void {
    for (const ws of this.sockets) {
      try {
        ws.close();
      } catch {
        /* best-effort */
      }
    }
    this.sockets.clear();
    this.wss?.close();
    this.server?.close();
    if (this.lockPath) {
      try {
        fs.unlinkSync(this.lockPath);
      } catch {
        /* lock may already be gone */
      }
      this.lockPath = '';
    }
  }
}

/** Wrap a tool payload (or error text) in the MCP result envelope. */
function envelope(payload: unknown, isError: boolean): unknown {
  const text = typeof payload === 'string' ? payload : JSON.stringify(payload ?? {});
  return { content: [{ type: 'text', text }], isError };
}

/** One tools/list entry. Schemas are permissive — the Go side owns arg shaping. */
function toolSpec(name: string): unknown {
  return {
    name,
    description: `${name} (kitsoki-vscode)`,
    inputSchema: { type: 'object', properties: {} },
  };
}
