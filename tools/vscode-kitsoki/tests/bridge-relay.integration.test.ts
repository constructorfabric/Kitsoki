// bridge-relay.integration.test.ts — wires the REAL webview BridgeTransport to
// the REAL host Relay over an in-process postMessage channel, against a stub
// HTTP/SSE server standing in for `kitsoki web`. No real VS Code, no LLM.
//
// This is the guard against wire-format drift: BridgeTransport (the SPA side,
// in tools/runstatus) and Relay (the host side, here) each have their own unit
// tests with their own mocks, so they can both go green while being mutually
// incompatible. This test fails the moment the two envelope codecs disagree.
//
// Run: node --test --import tsx tests/bridge-relay.integration.test.ts

import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as http from 'node:http';
import { Relay } from '../src/relay';
import { BridgeTransport } from '../../runstatus/src/transport/bridge-transport';

/** Minimal VS Code webview api the BridgeTransport posts through (inlined to
 *  avoid a cross-package type-only import that trips TS1541 under CJS). */
type VsCodeApiLike = { postMessage(message: unknown): void };

function startServer(
  handler: http.RequestListener,
): Promise<{ base: string; close: () => Promise<void> }> {
  return new Promise((resolve) => {
    const srv = http.createServer(handler);
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address();
      const port = typeof addr === 'object' && addr ? addr.port : 0;
      resolve({
        base: `http://127.0.0.1:${port}`,
        close: () =>
          new Promise<void>((res) => {
            srv.closeAllConnections?.();
            srv.close(() => res());
          }),
      });
    });
  });
}

/**
 * Wire a BridgeTransport to a Relay in-process:
 *   bridge.postMessage(env)  -> relay.handle(env)        (webview -> host)
 *   relay.post(env)          -> bridge's "message" event (host -> webview)
 * Each direction hops a microtask so it mimics the async postMessage boundary.
 */
function wire(base: string): { bridge: BridgeTransport; relay: Relay; dispose: () => void } {
  let bridgeListener: ((ev: { data: unknown }) => void) | null = null;

  const target = {
    addEventListener: (_t: string, fn: (ev: { data: unknown }) => void) => {
      bridgeListener = fn;
    },
    removeEventListener: () => {},
    dispatchEvent: () => true,
  } as unknown as EventTarget;

  // Forward-declare so the api can reach the relay created just below.
  let relay: Relay;

  const api: VsCodeApiLike = {
    postMessage: (msg: unknown) => {
      // webview -> host
      queueMicrotask(() => relay.handle(msg as Parameters<Relay['handle']>[0]));
    },
  };

  relay = new Relay({
    base,
    post: (env) => {
      // host -> webview
      queueMicrotask(() => bridgeListener?.({ data: env }));
    },
  });

  const bridge = new BridgeTransport(api, target);
  return { bridge, relay, dispose: () => relay.dispose() };
}

test('call() round-trips through bridge -> relay -> stub -> back', async () => {
  const srv = await startServer((req, res) => {
    let raw = '';
    req.on('data', (c) => (raw += c));
    req.on('end', () => {
      const body = JSON.parse(raw);
      res.setHeader('content-type', 'application/json');
      res.end(JSON.stringify({ jsonrpc: '2.0', id: body.id, result: { echoed: body.params } }));
    });
  });
  const { bridge, dispose } = wire(srv.base);

  const out = await bridge.call<{ echoed: unknown }>('runstatus.sessions.list', { a: 1 }, 1);
  assert.deepEqual(out, { echoed: { a: 1 } });

  dispose();
  await srv.close();
});

test('call() surfaces a JSON-RPC error frame as a rejected JsonRpcError', async () => {
  const srv = await startServer((req, res) => {
    let raw = '';
    req.on('data', (c) => (raw += c));
    req.on('end', () => {
      const body = JSON.parse(raw);
      res.setHeader('content-type', 'application/json');
      res.end(
        JSON.stringify({ jsonrpc: '2.0', id: body.id, error: { code: -32601, message: 'no method' } }),
      );
    });
  });
  const { bridge, dispose } = wire(srv.base);

  await assert.rejects(bridge.call('nope', {}, 1), (e: Error & { code?: number }) => {
    assert.equal(e.code, -32601);
    assert.match(e.message, /no method/);
    return true;
  });

  dispose();
  await srv.close();
});

test('openEventStream() delivers the RAW SSE data string for the data layer to parse', async () => {
  const frame = { method: 'runstatus.event', params: { event: { turn: 1 } } };
  let serverRes: http.ServerResponse | undefined;
  const srv = await startServer((req, res) => {
    assert.ok(req.url?.startsWith('/rpc/events'));
    res.setHeader('content-type', 'text/event-stream');
    serverRes = res;
    res.write(`data: ${JSON.stringify(frame)}\n\n`);
  });
  const { bridge, dispose } = wire(srv.base);

  const got = await new Promise<string>((resolve) => {
    const unsub = bridge.openEventStream('rpc/events', { subscription_id: 's1' }, {
      onMessage: (data) => {
        resolve(data);
        unsub();
      },
    });
  });
  // The webview side receives the verbatim string and parses it itself.
  assert.equal(got, JSON.stringify(frame));
  assert.deepEqual(JSON.parse(got), frame);

  serverRes?.end();
  dispose();
  await srv.close();
});

test('postEventStream() streams frames then resolves via the caller reduce on the done sentinel', async () => {
  const srv = await startServer((req, res) => {
    assert.equal(req.url, '/rpc/turn-stream');
    res.setHeader('content-type', 'text/event-stream');
    res.write(`data: ${JSON.stringify({ type: 'delta', text: 'a' })}\n\n`);
    res.write(`data: ${JSON.stringify({ type: 'delta', text: 'b' })}\n\n`);
    res.write(`data: ${JSON.stringify({ type: 'done', result: { ok: true } })}\n\n`);
    res.end();
  });
  const { bridge, dispose } = wire(srv.base);

  const frames: Record<string, unknown>[] = [];
  const result = await bridge.postEventStream<{ ok: boolean }>(
    'rpc/turn-stream',
    { session_id: 's1', method: 'turn', input: 'go' },
    {
      onFrame: (f) => frames.push(f),
      reduce: (f) => (f['type'] === 'done' ? { result: f['result'] as { ok: boolean } } : undefined),
    },
  );

  assert.deepEqual(frames, [
    { type: 'delta', text: 'a' },
    { type: 'delta', text: 'b' },
  ]);
  assert.deepEqual(result, { ok: true });

  dispose();
  await srv.close();
});
