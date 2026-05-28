/**
 * Unit tests for src/transport/jsonrpc.ts
 *
 * Uses a tiny MockEventSource that exposes emit() for test injection.
 * No real network calls; fetch and EventSource are fully mocked.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { JsonRpcClient, JsonRpcError } from "../../src/transport/jsonrpc.js";
import type { TraceEvent } from "../../src/types.js";

/** Flush all pending microtasks (works with real or fake timers). */
async function flushMicrotasks(): Promise<void> {
  // Multiple ticks to drain chained .then() chains.
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

// ---- MockEventSource -------------------------------------------------------

type ESListener = (ev: MessageEvent | Event) => void;

class MockEventSource {
  static instances: MockEventSource[] = [];

  readonly url: string;
  private listeners: Record<string, ESListener[]> = {};
  closed = false;

  constructor(url: string) {
    this.url = url;
    MockEventSource.instances.push(this);
  }

  addEventListener(type: string, fn: ESListener): void {
    (this.listeners[type] ??= []).push(fn);
  }

  // Allow onmessage / onerror assignment (EventSource interface).
  set onmessage(fn: ESListener) {
    this.listeners["message"] = [fn];
  }

  set onerror(fn: ESListener) {
    this.listeners["error"] = [fn];
  }

  close(): void {
    this.closed = true;
  }

  /** Test helper: inject a message event. */
  emit(type: string, data?: string): void {
    const fns = this.listeners[type] ?? [];
    for (const fn of fns) {
      if (type === "message") {
        fn({ data } as MessageEvent);
      } else {
        fn(new Event(type));
      }
    }
  }
}

// ---- Helpers ---------------------------------------------------------------

function makeResponse<T>(result: T, id = 1): Response {
  return new Response(
    JSON.stringify({ jsonrpc: "2.0", id, result }),
    { status: 200, headers: { "Content-Type": "application/json" } }
  );
}

function makeErrorResponse(
  id: number,
  code: number,
  message: string,
  data?: unknown
): Response {
  return new Response(
    JSON.stringify({ jsonrpc: "2.0", id, error: { code, message, data } }),
    { status: 200, headers: { "Content-Type": "application/json" } }
  );
}

function makeTraceEvent(turn: number): TraceEvent {
  return {
    time: new Date().toISOString(),
    level: "info",
    msg: `event at turn ${turn}`,
    session_id: "s1",
    turn,
    state_path: "root/state",
    attrs: {},
  };
}

// ---- Tests -----------------------------------------------------------------

describe("JsonRpcClient.post", () => {
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

  it("sends a well-formed JSON-RPC request", async () => {
    fetchMock.mockResolvedValueOnce(makeResponse({ ok: true }));
    const client = new JsonRpcClient("/");
    await client.post("runstatus.sessions.list", {});

    expect(fetchMock).toHaveBeenCalledOnce();
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/rpc");
    const body = JSON.parse(init.body as string) as {
      jsonrpc: string;
      id: number;
      method: string;
      params: unknown;
    };
    expect(body.jsonrpc).toBe("2.0");
    expect(body.method).toBe("runstatus.sessions.list");
    expect(typeof body.id).toBe("number");
  });

  it("correlates concurrent requests to their own results", async () => {
    // Two concurrent posts; responses arrive in reverse order.
    let resolve1!: (r: Response) => void;
    let resolve2!: (r: Response) => void;
    const p1 = new Promise<Response>((res) => { resolve1 = res; });
    const p2 = new Promise<Response>((res) => { resolve2 = res; });

    fetchMock
      .mockReturnValueOnce(p1)
      .mockReturnValueOnce(p2);

    const client = new JsonRpcClient("/");

    const postA = client.post<{ name: string }>("method.a", {});
    const postB = client.post<{ name: string }>("method.b", {});

    // Capture ids from the two calls.
    const bodyA = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { id: number };
    const bodyB = JSON.parse(
      (fetchMock.mock.calls[1] as [string, RequestInit])[1].body as string
    ) as { id: number };

    expect(bodyA.id).not.toBe(bodyB.id);

    // Resolve in reverse — B first.
    resolve2(makeResponse({ name: "B" }, bodyB.id));
    resolve1(makeResponse({ name: "A" }, bodyA.id));

    const [rA, rB] = await Promise.all([postA, postB]);
    expect(rA.name).toBe("A");
    expect(rB.name).toBe("B");
  });

  it("throws JsonRpcError for error frames", async () => {
    fetchMock.mockResolvedValueOnce(makeErrorResponse(1, -32600, "Invalid Request", { detail: "x" }));
    const client = new JsonRpcClient("/");
    await expect(client.post("bad.method", {})).rejects.toSatisfy(
      (e: unknown) =>
        e instanceof JsonRpcError &&
        e.code === -32600 &&
        e.message === "Invalid Request" &&
        (e.data as { detail: string }).detail === "x"
    );
  });

  it("throws JsonRpcError for non-200 HTTP status", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response("Not Found", { status: 404, statusText: "Not Found" })
    );
    const client = new JsonRpcClient("/");
    await expect(client.post("any.method", {})).rejects.toSatisfy(
      (e: unknown) => e instanceof JsonRpcError && e.code === 404
    );
  });
});

describe("JsonRpcClient.subscribe", () => {
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

  it("opens an EventSource after subscribing and delivers events", async () => {
    // subscribe RPC → subscription_id
    fetchMock.mockResolvedValueOnce(
      makeResponse({ subscription_id: "sub-1" })
    );

    const received: TraceEvent[] = [];
    const getTrace = vi.fn().mockResolvedValue({ events: [], last_turn: 0 });
    const client = new JsonRpcClient("/");
    client.subscribe("s1", (e) => received.push(e), getTrace);

    // Flush microtasks so the subscribe post resolves.
    await new Promise<void>((r) => setTimeout(r, 0));

    expect(MockEventSource.instances).toHaveLength(1);
    const es = MockEventSource.instances[0]!;
    expect(es.url).toContain("subscription_id=sub-1");

    // Inject an event.
    const evt = makeTraceEvent(1);
    es.emit(
      "message",
      JSON.stringify({
        jsonrpc: "2.0",
        method: "runstatus.event",
        params: { subscription_id: "sub-1", event: evt },
      })
    );

    expect(received).toHaveLength(1);
    expect(received[0]!.turn).toBe(1);
  });

  it("unsubscribe closes the EventSource and posts unsubscribe RPC", async () => {
    fetchMock
      .mockResolvedValueOnce(makeResponse({ subscription_id: "sub-2" })) // subscribe
      .mockResolvedValueOnce(makeResponse({ ok: true })); // unsubscribe

    const getTrace = vi.fn().mockResolvedValue({ events: [], last_turn: 0 });
    const client = new JsonRpcClient("/");
    const unsub = client.subscribe("s1", () => undefined, getTrace);

    await new Promise<void>((r) => setTimeout(r, 0));

    const es = MockEventSource.instances[0]!;
    unsub();

    expect(es.closed).toBe(true);
    // Give unsubscribe post time to fire.
    await new Promise<void>((r) => setTimeout(r, 0));

    const calls = fetchMock.mock.calls as [string, RequestInit][];
    const unsubCall = calls.find((c) => {
      const body = JSON.parse(c[1].body as string) as { method: string };
      return body.method === "runstatus.session.unsubscribe";
    });
    expect(unsubCall).toBeDefined();
  });

  it("reconnects with backoff on EventSource error and backfills via getTrace", async () => {
    vi.useFakeTimers();

    fetchMock.mockResolvedValue(makeResponse({ subscription_id: "sub-3" }));

    const received: TraceEvent[] = [];
    const backfillEvents = [makeTraceEvent(2), makeTraceEvent(3)];
    const getTrace = vi
      .fn()
      .mockResolvedValue({ events: backfillEvents, last_turn: 3 });

    const client = new JsonRpcClient("/");
    client.subscribe("s1", (e) => received.push(e), getTrace);

    // Flush subscribe post.
    await flushMicrotasks();
    // Let the EventSource open.
    await Promise.resolve();

    const firstEs = MockEventSource.instances[0]!;

    // Inject one event so lastTurn = 1.
    firstEs.emit(
      "message",
      JSON.stringify({
        jsonrpc: "2.0",
        method: "runstatus.event",
        params: { subscription_id: "sub-3", event: makeTraceEvent(1) },
      })
    );
    expect(received).toHaveLength(1);

    // Simulate an EventSource error → triggers reconnect logic.
    firstEs.emit("error");

    // Advance past the first backoff step (250 ms).
    await vi.advanceTimersByTimeAsync(300);
    await flushMicrotasks();

    // getTrace should have been called with since_turn = 2 (lastTurn + 1).
    expect(getTrace).toHaveBeenCalledWith(2);

    // Backfill events should have been delivered.
    expect(received).toHaveLength(3); // original 1 + backfill 2

    // A second EventSource should have been opened.
    expect(MockEventSource.instances).toHaveLength(2);

    vi.useRealTimers();
  });

  it("does not deliver duplicate events on reconnect", async () => {
    vi.useFakeTimers();

    fetchMock.mockResolvedValue(makeResponse({ subscription_id: "sub-4" }));

    const received: TraceEvent[] = [];
    // Backfill returns an event with the same turn as the last delivered.
    const getTrace = vi
      .fn()
      .mockResolvedValue({ events: [makeTraceEvent(1)], last_turn: 1 });

    const client = new JsonRpcClient("/");
    client.subscribe("s1", (e) => received.push(e), getTrace);

    await flushMicrotasks();
    await Promise.resolve();

    const firstEs = MockEventSource.instances[0]!;

    // Deliver turn 1 via stream.
    firstEs.emit(
      "message",
      JSON.stringify({
        jsonrpc: "2.0",
        method: "runstatus.event",
        params: { subscription_id: "sub-4", event: makeTraceEvent(1) },
      })
    );

    // Trigger reconnect.
    firstEs.emit("error");
    await vi.advanceTimersByTimeAsync(300);
    await flushMicrotasks();

    // Turn 1 should only appear once — backfill filtered by > lastTurn.
    const turn1 = received.filter((e) => e.turn === 1);
    expect(turn1).toHaveLength(1);

    vi.useRealTimers();
  });
});
