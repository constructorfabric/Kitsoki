// ide-server.unit.test.ts — protocol conformance for the IDE-MCP server.
//
// The kitsoki backend (internal/ide, Go) is the MCP client; this proves the
// extension's server speaks the exact contract that client expects: the
// initialize / notifications/initialized / tools/list handshake, the
// {content:[{type:"text",text:<json>}],isError} tools/call envelope, auth
// rejection, and lock-file lifecycle. The Go client is independently verified
// against an identical-contract stub (internal/ide/stubserver_test.go), so
// matching that contract here gives cross-language confidence without launching
// a real editor (the full path is exercised by the e2e tour).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as os from 'node:os';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { WebSocket } from 'ws';
import { IdeServer, type ToolDispatcher } from '../src/ide-server';

const AUTH_HEADER = 'x-claude-code-ide-authorization';
const noopLog = { appendLine() {} };

class FakeTools implements ToolDispatcher {
  calls: Array<{ name: string; args: Record<string, unknown> }> = [];
  async dispatch(name: string, args: Record<string, unknown>): Promise<unknown> {
    this.calls.push({ name, args });
    if (name === 'openFile') return { ok: true };
    if (name === 'boom') throw new Error('kaboom');
    return { echoed: name };
  }
}

/** A tiny correlating JSON-RPC client over ws (mirrors the Go client's frames). */
function rpcClient(port: number, token: string) {
  const ws = new WebSocket(`ws://127.0.0.1:${port}`, {
    headers: { [AUTH_HEADER]: token },
  });
  const pending = new Map<number, (r: any) => void>();
  let nextId = 1;
  ws.on('message', (d) => {
    const f = JSON.parse(d.toString());
    if (f.id != null && pending.has(f.id)) {
      pending.get(f.id)!(f);
      pending.delete(f.id);
    }
  });
  const ready = new Promise<void>((res, rej) => {
    ws.on('open', () => res());
    ws.on('error', rej);
  });
  const call = (method: string, params?: unknown) =>
    new Promise<any>((res) => {
      const id = nextId++;
      pending.set(id, res);
      ws.send(JSON.stringify({ jsonrpc: '2.0', id, method, params }));
    });
  const notify = (method: string) =>
    ws.send(JSON.stringify({ jsonrpc: '2.0', method }));
  return { ws, ready, call, notify };
}

test('ide-server: handshake, tools/list, tools/call envelope', async (t) => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'kitsoki-ide-'));
  const tools = new FakeTools();
  const server = new IdeServer(noopLog, tools, {
    lockDir: tmp,
    workspaceFolders: ['/ws/root'],
  });
  t.after(() => server.dispose());
  const port = await server.ready;
  assert.ok(port, 'server should bind a port');

  // Lock file shape (what the backend's discovery parses).
  const lock = JSON.parse(fs.readFileSync(path.join(tmp, `${port}.lock`), 'utf8'));
  assert.equal(lock.transport, 'ws');
  assert.equal(lock.ideName, 'Visual Studio Code');
  assert.deepEqual(lock.workspaceFolders, ['/ws/root']);
  assert.ok(typeof lock.authToken === 'string' && lock.authToken.length > 0);

  const c = rpcClient(port!, lock.authToken);
  await c.ready;

  const init = await c.call('initialize', {
    protocolVersion: '2024-11-05',
    capabilities: {},
    clientInfo: { name: 'test', version: '1' },
  });
  assert.equal(init.result.protocolVersion, '2024-11-05');
  assert.ok(init.result.capabilities.tools, 'advertises tools capability');

  c.notify('notifications/initialized');

  const list = await c.call('tools/list', {});
  const names = list.result.tools.map((x: any) => x.name).sort();
  assert.deepEqual(names, [
    'getCurrentSelection',
    'getDiagnostics',
    'getOpenEditors',
    'openDiff',
    'openFile',
  ]);

  // tools/call openFile -> {content:[{type:text,text:<json>}], isError:false}.
  const ok = await c.call('tools/call', {
    name: 'openFile',
    arguments: { path: '/x/y.md' },
  });
  assert.equal(ok.result.isError, false);
  assert.equal(ok.result.content[0].type, 'text');
  assert.deepEqual(JSON.parse(ok.result.content[0].text), { ok: true });
  assert.deepEqual(tools.calls.at(-1), { name: 'openFile', args: { path: '/x/y.md' } });

  // A throwing tool surfaces as isError:true (a domain outcome, not a crash).
  const bad = await c.call('tools/call', { name: 'boom', arguments: {} });
  assert.equal(bad.result.isError, true);
  assert.match(bad.result.content[0].text, /kaboom/);

  // Unknown method -> JSON-RPC method-not-found.
  const nope = await c.call('frobnicate', {});
  assert.equal(nope.error.code, -32601);

  c.ws.close();
});

test('ide-server: rejects a bad auth token', async (t) => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'kitsoki-ide-'));
  const server = new IdeServer(noopLog, new FakeTools(), { lockDir: tmp });
  t.after(() => server.dispose());
  const port = await server.ready;
  const ws = new WebSocket(`ws://127.0.0.1:${port}`, {
    headers: { [AUTH_HEADER]: 'wrong-token' },
  });
  await assert.rejects(
    new Promise((res, rej) => {
      ws.on('open', () => res(undefined));
      ws.on('error', rej);
    }),
  );
});

test('ide-server: dispose removes the lock file', async () => {
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'kitsoki-ide-'));
  const server = new IdeServer(noopLog, new FakeTools(), { lockDir: tmp });
  const port = await server.ready;
  const lockPath = path.join(tmp, `${port}.lock`);
  assert.ok(fs.existsSync(lockPath));
  server.dispose();
  assert.ok(!fs.existsSync(lockPath));
});
