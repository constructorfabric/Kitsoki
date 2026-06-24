/**
 * Unit tests for src/transport/bridge-transport.ts and the createTransport
 * host-detection branch in src/transport/transport.ts.
 *
 * Drives BridgeTransport against a FAKE postMessage peer: a stubbed
 * acquireVsCodeApi whose postMessage records the webview→host envelope, plus a
 * scripted dispatcher (FakeHost) that posts host→webview replies back through a
 * stub EventTarget. No real VS Code, no network.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { BridgeTransport } from "../../src/transport/bridge-transport.js";
import { createTransport, HttpTransport } from "../../src/transport/transport.js";
import { JsonRpcError } from "../../src/transport/jsonrpc.js";

// ---- Fake postMessage peer -------------------------------------------------

interface Envelope {
  t: string;
  id: number;
  [k: string]: unknown;
}

/**
 * A scripted host: collects the envelopes the webview posts, and replays
 * host→webview reply messages through a fake EventTarget the BridgeTransport
 * listens on.
 */
class FakeHost {
  readonly sent: Envelope[] = [];
  private listener: ((ev: MessageEvent) => void) | null = null;

  /** The VS Code api stub the BridgeTransport posts through. */
  readonly api = {
    postMessage: (msg: unknown) => {
      this.sent.push(msg as Envelope);
    },
  };

  /** The EventTarget stub the BridgeTransport subscribes to for host replies. */
  readonly target: EventTarget = {
    addEventListener: ((_type: string, fn: EventListenerOrEventListenerObject) => {
      this.listener = fn as (ev: MessageEvent) => void;
    }) as EventTarget["addEventListener"],
    removeEventListener: (() => {}) as EventTarget["removeEventListener"],
    dispatchEvent: (() => true) as EventTarget["dispatchEvent"],
  };

  /** Push one host→webview reply into the BridgeTransport. */
  reply(msg: Record<string, unknown>): void {
    this.listener?.({ data: msg } as MessageEvent);
  }

  /** The last envelope of a given type (most recent op). */
  last(t: string): Envelope {
    const found = [...this.sent].reverse().find((e) => e.t === t);
    if (!found) throw new Error(`no ${t} envelope sent`);
    return found;
  }
}

function makeBridge(): { bridge: BridgeTransport; host: FakeHost } {
  const host = new FakeHost();
  const bridge = new BridgeTransport(host.api, host.target);
  return { bridge, host };
}

async function flush(): Promise<void> {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

// ---- call() ----------------------------------------------------------------

describe("BridgeTransport.call", () => {
  it("posts a call envelope and resolves on call-ok", async () => {
    const { bridge, host } = makeBridge();
    // The bridge is the shared singleton transport and mints its OWN wire id
    // (the caller-supplied 7 is ignored) so two JsonRpcClients can't collide on
    // id=1. Read the id it actually sent and reply on that.
    const p = bridge.call<{ ok: boolean }>("runstatus.sessions.list", { a: 1 }, 7);

    const env = host.last("call");
    expect(env).toMatchObject({
      t: "call",
      method: "runstatus.sessions.list",
      params: { a: 1 },
    });
    expect(typeof env.id).toBe("number");

    host.reply({ t: "call-ok", id: env.id, result: { ok: true } });
    await expect(p).resolves.toEqual({ ok: true });
  });

  it("rejects with JsonRpcError on call-err and records lastError", async () => {
    const { bridge, host } = makeBridge();
    const p = bridge.call("runstatus.session.get", {}, 3);

    host.reply({
      t: "call-err",
      id: host.last("call").id,
      error: { code: -32001, message: "boom", data: { detail: "x" } },
    });

    await expect(p).rejects.toBeInstanceOf(JsonRpcError);
    await p.catch((e: JsonRpcError) => {
      expect(e.code).toBe(-32001);
      expect(e.message).toBe("boom");
      expect(e.data).toEqual({ detail: "x" });
    });
    expect(bridge.getLastError()).toEqual({
      method: "runstatus.session.get",
      code: -32001,
      message: "boom",
    });
  });

  it("ignores a reply whose id does not match a pending call", async () => {
    const { bridge, host } = makeBridge();
    const p = bridge.call("m", {}, 1);
    const id = host.last("call").id;
    host.reply({ t: "call-ok", id: 999, result: "wrong" });
    host.reply({ t: "call-ok", id, result: "right" });
    await expect(p).resolves.toBe("right");
  });
});

// ---- openEventStream() -----------------------------------------------------

describe("BridgeTransport.openEventStream", () => {
  it("fans evt-msg frames to onMessage and sends evt-close on unsubscribe", async () => {
    const { bridge, host } = makeBridge();
    const seen: string[] = [];
    const errs: unknown[] = [];

    const unsub = bridge.openEventStream(
      "rpc/events",
      { subscription_id: "sub-1" },
      { onMessage: (d) => seen.push(d), onError: (e) => errs.push(e) }
    );

    const open = host.last("evt-open");
    expect(open).toMatchObject({
      t: "evt-open",
      path: "rpc/events",
      query: { subscription_id: "sub-1" },
    });
    const streamId = open.id;

    host.reply({ t: "evt-msg", id: streamId, data: "frame-a" });
    host.reply({ t: "evt-msg", id: streamId, data: "frame-b" });
    expect(seen).toEqual(["frame-a", "frame-b"]);

    // Errors surface but the webview does NOT reconnect (host owns it).
    host.reply({ t: "evt-err", id: streamId, error: { message: "blip" } });
    expect(errs).toHaveLength(1);

    unsub();
    const close = host.last("evt-close");
    expect(close).toMatchObject({ t: "evt-close", id: streamId });

    // After unsubscribe, further frames are dropped.
    host.reply({ t: "evt-msg", id: streamId, data: "late" });
    expect(seen).toEqual(["frame-a", "frame-b"]);
  });
});

// ---- postEventStream() -----------------------------------------------------

describe("BridgeTransport.postEventStream", () => {
  it("streams post-frame frames and resolves on post-done via reduce", async () => {
    const { bridge, host } = makeBridge();
    const frames: Record<string, unknown>[] = [];

    const p = bridge.postEventStream<{ value: number }>(
      "rpc/turn-stream",
      { session_id: "s1", method: "turn", input: "go" },
      {
        onFrame: (f) => frames.push(f),
        reduce: (f) =>
          f["type"] === "done"
            ? { result: { value: f["value"] as number } }
            : undefined,
      }
    );

    const open = host.last("post-open");
    expect(open).toMatchObject({
      t: "post-open",
      path: "rpc/turn-stream",
      body: { session_id: "s1", method: "turn", input: "go" },
    });
    const postId = open.id;

    host.reply({ t: "post-frame", id: postId, frame: { type: "delta", text: "a" } });
    host.reply({ t: "post-frame", id: postId, frame: { type: "tool", tool: "x" } });
    expect(frames).toEqual([
      { type: "delta", text: "a" },
      { type: "tool", tool: "x" },
    ]);

    host.reply({ t: "post-done", id: postId, frame: { type: "done", value: 42 } });
    await expect(p).resolves.toEqual({ value: 42 });
  });

  it("rejects on post-err", async () => {
    const { bridge, host } = makeBridge();
    const p = bridge.postEventStream(
      "rpc/meta-stream",
      {},
      { onFrame: () => {}, reduce: () => undefined }
    );
    const postId = host.last("post-open").id;
    host.reply({ t: "post-err", id: postId, error: { message: "stream failed" } });
    await expect(p).rejects.toThrow("stream failed");
  });

  it("rejects when post-done's frame does not reduce to a terminal", async () => {
    const { bridge, host } = makeBridge();
    const p = bridge.postEventStream(
      "rpc/turn-stream",
      {},
      { onFrame: () => {}, reduce: () => undefined }
    );
    const postId = host.last("post-open").id;
    host.reply({ t: "post-done", id: postId, frame: { type: "done" } });
    await expect(p).rejects.toThrow(/terminal/);
  });
});

// ---- createTransport() factory ---------------------------------------------

describe("createTransport host detection", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("returns BridgeTransport when acquireVsCodeApi is defined", () => {
    vi.stubGlobal("acquireVsCodeApi", () => ({ postMessage: () => {} }));
    const t = createTransport("/");
    expect(t).toBeInstanceOf(BridgeTransport);
  });

  it("returns the SAME BridgeTransport on repeated calls (singleton)", () => {
    // Regression: the SPA constructs ~15 LiveSource instances, each calling
    // createTransport(). acquireVsCodeApi() throws if invoked more than once, so
    // a fresh BridgeTransport per call would crash the webview boot ("An instance
    // of the VS Code API has already been acquired"). The factory must return one
    // shared instance.
    let acquireCount = 0;
    vi.stubGlobal("acquireVsCodeApi", () => {
      acquireCount += 1;
      return { postMessage: () => {} };
    });
    const a = createTransport("/");
    const b = createTransport("/");
    expect(a).toBe(b);
    expect(a).toBeInstanceOf(BridgeTransport);
    // acquireVsCodeApi must have been called at most once across both factory
    // invocations (it is cached after the first BridgeTransport ever built).
    expect(acquireCount).toBeLessThanOrEqual(1);
  });

  it("returns HttpTransport when acquireVsCodeApi is absent", () => {
    // Ensure no global is present.
    expect(typeof (globalThis as Record<string, unknown>)["acquireVsCodeApi"]).toBe(
      "undefined"
    );
    const t = createTransport("/");
    expect(t).toBeInstanceOf(HttpTransport);
  });
});

beforeEach(() => {
  // Each test that doesn't stub acquireVsCodeApi expects it absent.
  delete (globalThis as Record<string, unknown>)["acquireVsCodeApi"];
});
