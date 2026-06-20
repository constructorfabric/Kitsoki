// relay.unit.test.ts — exercises the host-side envelope <-> fetch/SSE
// translation against a plain stub HTTP server. No real kitsoki, no LLM.
//
// Run: node --test --import tsx tests/relay.unit.test.ts

import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as http from 'node:http';
import { Relay, type OutboundEnvelope } from '../src/relay';

/** Spin up an ephemeral HTTP server with the given handler; return base + close. */
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

/** Collect posts; resolve when a predicate matches one. */
function collector() {
  const posts: OutboundEnvelope[] = [];
  const waiters: { pred: (e: OutboundEnvelope) => boolean; resolve: (e: OutboundEnvelope) => void }[] = [];
  return {
    posts,
    post(env: OutboundEnvelope) {
      posts.push(env);
      for (let i = waiters.length - 1; i >= 0; i--) {
        if (waiters[i].pred(env)) {
          waiters[i].resolve(env);
          waiters.splice(i, 1);
        }
      }
    },
    waitFor(pred: (e: OutboundEnvelope) => boolean, timeoutMs = 3000): Promise<OutboundEnvelope> {
      const existing = posts.find(pred);
      if (existing) return Promise.resolve(existing);
      return new Promise((resolve, reject) => {
        const t = setTimeout(() => reject(new Error('waitFor timed out')), timeoutMs);
        waiters.push({
          pred,
          resolve: (e) => {
            clearTimeout(t);
            resolve(e);
          },
        });
      });
    },
  };
}

test('call envelope issues POST /rpc and resolves call-ok with the result', async () => {
  let sawBody: unknown;
  const srv = await startServer((req, res) => {
    assert.equal(req.method, 'POST');
    assert.equal(req.url, '/rpc');
    let raw = '';
    req.on('data', (c) => (raw += c));
    req.on('end', () => {
      sawBody = JSON.parse(raw);
      res.setHeader('content-type', 'application/json');
      res.end(JSON.stringify({ jsonrpc: '2.0', id: 7, result: { ok: true, echo: 42 } }));
    });
  });
  const col = collector();
  const relay = new Relay({ base: srv.base, post: col.post });

  relay.handle({ t: 'call', id: 7, method: 'doThing', params: { n: 42 } });
  const out = await col.waitFor((e) => e.t === 'call-ok' && e.id === 7);

  assert.deepEqual(sawBody, { jsonrpc: '2.0', id: 7, method: 'doThing', params: { n: 42 } });
  assert.equal(out.t, 'call-ok');
  assert.deepEqual((out as Extract<OutboundEnvelope, { t: 'call-ok' }>).result, { ok: true, echo: 42 });

  relay.dispose();
  await srv.close();
});

test('a JSON-RPC error frame yields call-err', async () => {
  const srv = await startServer((req, res) => {
    let raw = '';
    req.on('data', (c) => (raw += c));
    req.on('end', () => {
      res.setHeader('content-type', 'application/json');
      res.end(
        JSON.stringify({ jsonrpc: '2.0', id: 9, error: { code: -32601, message: 'no such method' } }),
      );
    });
  });
  const col = collector();
  const relay = new Relay({ base: srv.base, post: col.post });

  relay.handle({ t: 'call', id: 9, method: 'nope', params: {} });
  const out = await col.waitFor((e) => e.t === 'call-err' && e.id === 9);

  assert.equal(out.t, 'call-err');
  const err = (out as Extract<OutboundEnvelope, { t: 'call-err' }>).error;
  assert.equal(err.code, -32601);
  assert.equal(err.message, 'no such method');

  relay.dispose();
  await srv.close();
});

test('post-open forwards a post-frame then resolves on the done sentinel', async () => {
  const srv = await startServer((req, res) => {
    assert.equal(req.method, 'POST');
    assert.equal(req.url, '/rpc/turn-stream');
    res.setHeader('content-type', 'text/event-stream');
    res.setHeader('cache-control', 'no-cache');
    res.write(`data: ${JSON.stringify({ type: 'frame', step: 1 })}\n\n`);
    res.write(`data: ${JSON.stringify({ type: 'frame', step: 2 })}\n\n`);
    res.write(`data: ${JSON.stringify({ type: 'done', result: { final: true } })}\n\n`);
    res.end();
  });
  const col = collector();
  const relay = new Relay({ base: srv.base, post: col.post });

  relay.handle({ t: 'post-open', id: 3, path: 'rpc/turn-stream', body: { intent: 'go' } });

  const frame = await col.waitFor((e) => e.t === 'post-frame' && e.id === 3);
  assert.deepEqual((frame as Extract<OutboundEnvelope, { t: 'post-frame' }>).frame, {
    type: 'frame',
    step: 1,
  });

  const done = await col.waitFor((e) => e.t === 'post-done' && e.id === 3);
  assert.deepEqual((done as Extract<OutboundEnvelope, { t: 'post-done' }>).frame, {
    type: 'done',
    result: { final: true },
  });

  // No post-err should have been emitted for this id.
  assert.ok(!col.posts.some((e) => e.t === 'post-err' && e.id === 3));

  relay.dispose();
  await srv.close();
});

test('an error frame in a post stream rejects via post-err', async () => {
  const srv = await startServer((req, res) => {
    res.setHeader('content-type', 'text/event-stream');
    res.write(`data: ${JSON.stringify({ type: 'error', message: 'boom' })}\n\n`);
    res.end();
  });
  const col = collector();
  const relay = new Relay({ base: srv.base, post: col.post });

  relay.handle({ t: 'post-open', id: 5, path: 'rpc/turn-stream', body: {} });
  const out = await col.waitFor((e) => e.t === 'post-err' && e.id === 5);
  assert.equal((out as Extract<OutboundEnvelope, { t: 'post-err' }>).error.message, 'boom');

  relay.dispose();
  await srv.close();
});

test('after a backend restart, resetStreams + setBase re-points calls at the new port', async () => {
  // Old backend: would answer if still addressed. New backend: the only one the
  // relay must talk to after the restart re-point.
  const oldHits: string[] = [];
  const oldSrv = await startServer((req, res) => {
    oldHits.push(req.url ?? '');
    res.setHeader('content-type', 'application/json');
    res.end(JSON.stringify({ jsonrpc: '2.0', id: 1, result: { from: 'old' } }));
  });
  const newSrv = await startServer((req, res) => {
    let raw = '';
    req.on('data', (c) => (raw += c));
    req.on('end', () => {
      res.setHeader('content-type', 'application/json');
      res.end(JSON.stringify({ jsonrpc: '2.0', id: JSON.parse(raw).id, result: { from: 'new' } }));
    });
  });

  const col = collector();
  const relay = new Relay({ base: oldSrv.base, post: col.post });

  // Simulate the restart re-point that mountSpa performs on backend.onDidRestart.
  relay.resetStreams();
  relay.setBase(newSrv.base);

  relay.handle({ t: 'call', id: 1, method: 'doThing', params: {} });
  const out = await col.waitFor((e) => e.t === 'call-ok' && e.id === 1);
  assert.deepEqual((out as Extract<OutboundEnvelope, { t: 'call-ok' }>).result, { from: 'new' });
  // The dead old port must never be addressed after the re-point.
  assert.deepEqual(oldHits, []);

  relay.dispose();
  await oldSrv.close();
  await newSrv.close();
});

test('resetStreams aborts an in-flight SSE stream without retiring the relay', async () => {
  let opens = 0;
  const srv = await startServer((req, res) => {
    if (req.url?.startsWith('/rpc/events')) {
      opens++;
      res.setHeader('content-type', 'text/event-stream');
      res.write(`data: ${JSON.stringify({ method: 'tick', params: { i: opens } })}\n\n`);
      return; // held open
    }
    res.statusCode = 404;
    res.end();
  });
  const col = collector();
  const relay = new Relay({ base: srv.base, post: col.post });

  relay.handle({ t: 'evt-open', id: 1, path: 'rpc/events', query: {} });
  await col.waitFor((e) => e.t === 'evt-msg' && e.id === 1);

  // Abort in-flight streams (the restart path) — the relay must stay usable.
  relay.resetStreams();

  // A fresh stream still works on the same (or re-pointed) relay.
  relay.handle({ t: 'evt-open', id: 2, path: 'rpc/events', query: {} });
  const msg = await col.waitFor((e) => e.t === 'evt-msg' && e.id === 2);
  assert.equal(
    (msg as Extract<OutboundEnvelope, { t: 'evt-msg' }>).data,
    JSON.stringify({ method: 'tick', params: { i: 2 } }),
  );

  relay.dispose();
  await srv.close();
});

test('evt-open forwards SSE frames as evt-msg and evt-close stops it', async () => {
  let serverRes: http.ServerResponse | undefined;
  const srv = await startServer((req, res) => {
    assert.equal(req.url?.startsWith('/rpc/events'), true);
    res.setHeader('content-type', 'text/event-stream');
    serverRes = res;
    res.write(`data: ${JSON.stringify({ method: 'tick', params: { i: 1 } })}\n\n`);
  });
  const col = collector();
  const relay = new Relay({ base: srv.base, post: col.post });

  relay.handle({ t: 'evt-open', id: 11, path: 'rpc/events', query: { session: 'abc' } });
  const msg = await col.waitFor((e) => e.t === 'evt-msg' && e.id === 11);
  // evt-msg carries the RAW SSE `data:` payload string (the webview data layer
  // JSON.parses it — exactly as HttpTransport passes EventSource ev.data through).
  assert.equal(
    (msg as Extract<OutboundEnvelope, { t: 'evt-msg' }>).data,
    JSON.stringify({ method: 'tick', params: { i: 1 } }),
  );

  relay.handle({ t: 'evt-close', id: 11 });
  serverRes?.end();

  relay.dispose();
  await srv.close();
});
