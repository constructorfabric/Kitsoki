/**
 * Unit tests for src/data/live-source.ts
 *
 * Mocks fetch and a lightweight MockEventSource; validates the full
 * LiveSource → JsonRpcClient plumbing including subscribe reconnect.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { LiveSource } from "../../src/data/live-source.js";
import type { TraceEvent } from "../../src/types.js";

/** Flush all pending microtasks. */
async function flushMicrotasks(): Promise<void> {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

// ---- MockEventSource -------------------------------------------------------

type ESListener = (ev: MessageEvent | Event) => void;

class MockEventSource {
  static instances: MockEventSource[] = [];
  readonly url: string;
  private _onmessage: ESListener | null = null;
  private _onerror: ESListener | null = null;
  closed = false;

  constructor(url: string) {
    this.url = url;
    MockEventSource.instances.push(this);
  }

  set onmessage(fn: ESListener) { this._onmessage = fn; }
  set onerror(fn: ESListener) { this._onerror = fn; }

  addEventListener(type: string, fn: ESListener): void {
    if (type === "message") this._onmessage = fn;
    else if (type === "error") this._onerror = fn;
  }

  close(): void { this.closed = true; }

  emit(type: string, data?: string): void {
    if (type === "message" && this._onmessage)
      this._onmessage({ data } as MessageEvent);
    else if (type === "error" && this._onerror)
      this._onerror(new Event("error"));
  }
}

// ---- Helpers ---------------------------------------------------------------

function jsonResp<T>(body: T, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function rpcOk<T>(result: T, id = 1): Response {
  return jsonResp({ jsonrpc: "2.0", id, result });
}

function makeEvent(turn: number): TraceEvent {
  return {
    time: new Date().toISOString(),
    level: "info",
    msg: `turn ${turn}`,
    session_id: "s1",
    turn,
    state_path: "root/a",
    attrs: {},
  };
}

function notification(event: TraceEvent, subId = "sub-1"): string {
  return JSON.stringify({
    jsonrpc: "2.0",
    method: "runstatus.event",
    params: { subscription_id: subId, event },
  });
}

// ---- Tests -----------------------------------------------------------------

describe("LiveSource", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    MockEventSource.instances = [];
    vi.stubGlobal("EventSource", MockEventSource);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("listSessions calls runstatus.sessions.list", async () => {
    fetchMock.mockResolvedValueOnce(rpcOk([{ session_id: "s1" }]));
    const src = new LiveSource("/");
    const sessions = await src.listSessions();
    expect(sessions[0]!.session_id).toBe("s1");
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string };
    expect(body.method).toBe("runstatus.sessions.list");
  });

  it("getSession calls runstatus.session.get with session_id", async () => {
    const header = { session_id: "s1", app_id: "app", current_state: "root/a", turn: 1, started_at: "t", terminal: false };
    fetchMock.mockResolvedValueOnce(rpcOk(header));
    const src = new LiveSource("/");
    const result = await src.getSession("s1");
    expect(result.session_id).toBe("s1");
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string; params: { session_id: string } };
    expect(body.method).toBe("runstatus.session.get");
    expect(body.params.session_id).toBe("s1");
  });

  it("getApp calls runstatus.session.app", async () => {
    const appDef = { id: "app", root: "root", states: {} };
    fetchMock.mockResolvedValueOnce(rpcOk(appDef));
    const src = new LiveSource("/");
    const result = await src.getApp("s1");
    expect(result.id).toBe("app");
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string };
    expect(body.method).toBe("runstatus.session.app");
  });

  it("getMermaid calls runstatus.session.mermaid with optional detail", async () => {
    const mermaidSnap = { source: "flowchart LR", node_map: {} };
    fetchMock.mockResolvedValueOnce(rpcOk(mermaidSnap));
    const src = new LiveSource("/");
    await src.getMermaid("s1", "states");
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string; params: { detail?: string } };
    expect(body.method).toBe("runstatus.session.mermaid");
    expect(body.params.detail).toBe("states");
  });

  it("getTrace passes cursor params", async () => {
    fetchMock.mockResolvedValueOnce(rpcOk({ events: [], last_turn: 0 }));
    const src = new LiveSource("/");
    await src.getTrace("s1", { since_turn: 3, limit: 10 });
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string; params: { since_turn: number; limit: number } };
    expect(body.method).toBe("runstatus.session.trace");
    expect(body.params.since_turn).toBe(3);
    expect(body.params.limit).toBe(10);
  });

  it("subscribe lifecycle: events delivered, unsubscribe cleans up", async () => {
    fetchMock
      .mockResolvedValueOnce(rpcOk({ subscription_id: "sub-1" })) // subscribe
      .mockResolvedValueOnce(rpcOk({ ok: true })); // unsubscribe

    const received: TraceEvent[] = [];
    const src = new LiveSource("/");
    const unsub = src.subscribe("s1", (e) => received.push(e));

    await new Promise<void>((r) => setTimeout(r, 0));

    const es = MockEventSource.instances[0]!;
    es.emit("message", notification(makeEvent(1)));
    es.emit("message", notification(makeEvent(2)));

    expect(received).toHaveLength(2);
    expect(received[1]!.turn).toBe(2);

    unsub();
    expect(es.closed).toBe(true);

    await new Promise<void>((r) => setTimeout(r, 0));
    const calls = fetchMock.mock.calls as [string, RequestInit][];
    const unsubCall = calls.find((c) => {
      const b = JSON.parse(c[1].body as string) as { method: string };
      return b.method === "runstatus.session.unsubscribe";
    });
    expect(unsubCall).toBeDefined();
  });

  it("reconnect with backfill: delivers backfill events, no duplicates", async () => {
    vi.useFakeTimers();

    // All RPC calls return subscription_id (multiple reconnects).
    fetchMock.mockResolvedValue(rpcOk({ subscription_id: "sub-5" }));

    const received: TraceEvent[] = [];

    // getTrace for backfill returns turn 2.
    const backfillResult = { events: [makeEvent(2)], last_turn: 2 };
    // We mock the entire getTrace via a spy on the instance.
    // But LiveSource calls this.getTrace internally in subscribe, so we
    // intercept at the fetch level for trace calls.
    let traceCallCount = 0;
    fetchMock.mockImplementation((_url: string, init: RequestInit) => {
      const body = JSON.parse(init.body as string) as { method: string; id: number };
      if (body.method === "runstatus.session.subscribe") {
        return Promise.resolve(rpcOk({ subscription_id: "sub-5" }, body.id));
      }
      if (body.method === "runstatus.session.trace") {
        traceCallCount++;
        return Promise.resolve(rpcOk(backfillResult, body.id));
      }
      if (body.method === "runstatus.session.unsubscribe") {
        return Promise.resolve(rpcOk({ ok: true }, body.id));
      }
      return Promise.resolve(rpcOk({}, body.id));
    });

    const src = new LiveSource("/");
    src.subscribe("s1", (e) => received.push(e));

    await flushMicrotasks();
    await Promise.resolve();

    const firstEs = MockEventSource.instances[0]!;

    // Deliver turn 1 via stream — lastTurn = 1.
    firstEs.emit("message", notification(makeEvent(1)));
    expect(received).toHaveLength(1);

    // Simulate error → backoff → reconnect.
    firstEs.emit("error");
    await vi.advanceTimersByTimeAsync(300);
    await flushMicrotasks();

    // getTrace should have been called once for backfill.
    expect(traceCallCount).toBeGreaterThanOrEqual(1);

    // Backfill event (turn 2) delivered.
    expect(received.some((e) => e.turn === 2)).toBe(true);

    // Turn 1 appears only once.
    expect(received.filter((e) => e.turn === 1)).toHaveLength(1);

    vi.useRealTimers();
  });
});
