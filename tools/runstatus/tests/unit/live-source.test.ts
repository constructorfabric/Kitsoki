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

  it("listWork calls runstatus.work.list", async () => {
    fetchMock.mockResolvedValueOnce(
      rpcOk({
        summary: {
          items: 1,
          needs_attention: 0,
          jobs_running: 0,
          jobs_awaiting_input: 0,
          jobs_terminal: 0,
          notifications_unread: 0,
          notifications_action_required: 0,
          pending_drives: 1,
          backgrounded_chats: 0,
        },
        sessions: [],
        items: [{ kind: "pending_drive", priority: 65, session_id: "s1", reacquire_tool: "chat.show" }],
      })
    );
    const src = new LiveSource("/");
    const work = await src.listWork();
    expect(work.items[0]!.kind).toBe("pending_drive");
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string };
    expect(body.method).toBe("runstatus.work.list");
  });

  it("showChat calls runstatus.chat.show with session and chat ids", async () => {
    fetchMock.mockResolvedValueOnce(
      rpcOk({
        ok: true,
        chat: {
          id: "chat-1",
          app_id: "demo",
          room: "agent",
          scope_key: "scope",
          title: "Background Claude",
          status: "active",
          created_at_unix_micro: 1,
          updated_at_unix_micro: 2,
          last_active_at_unix_micro: 3,
        },
        messages: [{ chat_id: "chat-1", seq: 1, role: "assistant", content: "done", created_at_unix_micro: 4 }],
      })
    );
    const src = new LiveSource("/");
    const result = await src.showChat("s1", "chat-1", 1);
    expect(result.chat.title).toBe("Background Claude");
    expect(result.messages?.[0]?.content).toBe("done");
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string; params: { session_id: string; chat_id: string; since_seq: number } };
    expect(body.method).toBe("runstatus.chat.show");
    expect(body.params.session_id).toBe("s1");
    expect(body.params.chat_id).toBe("chat-1");
    expect(body.params.since_seq).toBe(1);
  });

  it("syncGitHubInbox calls the session GitHub inbox RPC", async () => {
    fetchMock.mockResolvedValueOnce(
      rpcOk({ ok: true, session_id: "s1", fetched: 1, inserted: 1, skipped: 0, items: [] })
    );
    const src = new LiveSource("/");
    const result = await src.syncGitHubInbox("s1", { repo: "acme/repo" });
    expect(result.inserted).toBe(1);
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string; params: { session_id: string; repo: string } };
    expect(body.method).toBe("runstatus.session.inbox.sync_github");
    expect(body.params.session_id).toBe("s1");
    expect(body.params.repo).toBe("acme/repo");
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

  it("view calls runstatus.session.view", async () => {
    fetchMock.mockResolvedValueOnce(
      rpcOk({ mode: "transitioned", state: "idle", turn_number: 1 })
    );
    const src = new LiveSource("/");
    const result = await src.view("s1");
    expect(result.state).toBe("idle");
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string };
    expect(body.method).toBe("runstatus.session.view");
  });

  it("submit calls runstatus.session.submit with intent + slots", async () => {
    fetchMock.mockResolvedValueOnce(
      rpcOk({ mode: "transitioned", state: "idle", turn_number: 2 })
    );
    const src = new LiveSource("/");
    await src.submit("s1", "discuss", { message: "hello" });
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string; params: { intent: string; slots: Record<string, unknown> } };
    expect(body.method).toBe("runstatus.session.submit");
    expect(body.params.intent).toBe("discuss");
    expect(body.params.slots).toEqual({ message: "hello" });
  });

  it("submit defaults slots to {} when omitted", async () => {
    fetchMock.mockResolvedValueOnce(
      rpcOk({ mode: "transitioned", state: "clarifying", turn_number: 2 })
    );
    const src = new LiveSource("/");
    await src.submit("s1", "start");
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { params: { slots: Record<string, unknown> } };
    expect(body.params.slots).toEqual({});
  });

  it("sendTurn calls runstatus.session.turn with input", async () => {
    fetchMock.mockResolvedValueOnce(
      rpcOk({ mode: "transitioned", state: "idle", turn_number: 2 })
    );
    const src = new LiveSource("/");
    await src.sendTurn("s1", "build me a thing");
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string; params: { input: string } };
    expect(body.method).toBe("runstatus.session.turn");
    expect(body.params.input).toBe("build me a thing");
  });

  it("continueTurn calls runstatus.session.continue with slots", async () => {
    fetchMock.mockResolvedValueOnce(
      rpcOk({ mode: "transitioned", state: "brief", turn_number: 3 })
    );
    const src = new LiveSource("/");
    await src.continueTurn("s1", { n: 2, text: "two" });
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string; params: { slots: Record<string, unknown> } };
    expect(body.method).toBe("runstatus.session.continue");
    expect(body.params.slots).toEqual({ n: 2, text: "two" });
  });

  it("offpath calls runstatus.session.offpath and returns the answer", async () => {
    fetchMock.mockResolvedValueOnce(rpcOk({ answer: "42" }));
    const src = new LiveSource("/");
    const out = await src.offpath("s1", "what is the meaning?");
    expect(out.answer).toBe("42");
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string; params: { input: string } };
    expect(body.method).toBe("runstatus.session.offpath");
    expect(body.params.input).toBe("what is the meaning?");
  });

  it("listStories calls runstatus.stories.list", async () => {
    fetchMock.mockResolvedValueOnce(
      rpcOk([{ path: "/a/app.yaml", app_id: "a", title: "A", active_sessions: [] }])
    );
    const src = new LiveSource("/");
    const stories = await src.listStories();
    expect(stories[0]!.path).toBe("/a/app.yaml");
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string };
    expect(body.method).toBe("runstatus.stories.list");
  });

  it("rescanStories calls runstatus.stories.rescan", async () => {
    fetchMock.mockResolvedValueOnce(rpcOk([]));
    const src = new LiveSource("/");
    await src.rescanStories();
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string };
    expect(body.method).toBe("runstatus.stories.rescan");
  });

  it("newSession calls runstatus.session.new with story_path and returns the id", async () => {
    fetchMock.mockResolvedValueOnce(rpcOk({ session_id: "sess-new" }));
    const src = new LiveSource("/");
    const id = await src.newSession("/abs/story/app.yaml");
    expect(id).toBe("sess-new");
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string; params: { story_path: string } };
    expect(body.method).toBe("runstatus.session.new");
    expect(body.params.story_path).toBe("/abs/story/app.yaml");
  });

  it("getTranscript calls runstatus.session.transcript and maps schema_version", async () => {
    fetchMock.mockResolvedValueOnce(
      rpcOk({
        format: "claude-stream-json",
        events: [{ type: "result", result: "ok" }],
        timings: [0],
        schema_version: 1,
      })
    );
    const src = new LiveSource("/");
    const tr = await src.getTranscript("s1", "4e96533378e89461");
    expect(tr.format).toBe("claude-stream-json");
    expect(tr.events).toHaveLength(1);
    expect(tr.schemaVersion).toBe(1);
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string; params: { call_id: string } };
    expect(body.method).toBe("runstatus.session.transcript");
    expect(body.params.call_id).toBe("4e96533378e89461");
  });

  it("reloadSession calls runstatus.session.reload and returns prev_state_exists", async () => {
    fetchMock.mockResolvedValueOnce(rpcOk({ ok: true, prev_state_exists: false }));
    const src = new LiveSource("/");
    const res = await src.reloadSession("s1");
    expect(res.ok).toBe(true);
    expect(res.prev_state_exists).toBe(false);
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string; params: { session_id: string } };
    expect(body.method).toBe("runstatus.session.reload");
    expect(body.params.session_id).toBe("s1");
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

  it("subscribeQuestions delivers runstatus.question frames and unsubscribes", async () => {
    fetchMock
      .mockResolvedValueOnce(rpcOk({ subscription_id: "q-sub-1" })) // subscribe
      .mockResolvedValueOnce(rpcOk({ ok: true })); // unsubscribe

    const frames: unknown[] = [];
    const src = new LiveSource("/");
    const unsub = src.subscribeQuestions((f) => frames.push(f));

    await new Promise<void>((r) => setTimeout(r, 0));

    const es = MockEventSource.instances[0]!;
    expect(es.url).toContain("rpc/questions?subscription_id=q-sub-1");
    es.emit(
      "message",
      JSON.stringify({
        jsonrpc: "2.0",
        method: "runstatus.question",
        params: {
          session_id: "pub-1",
          question_id: "q-7",
          questions: [{ question: "Ship?", header: "Ship", options: [{ label: "Yes" }] }],
        },
      })
    );
    // A non-question frame on the same stream is ignored.
    es.emit("message", JSON.stringify({ method: "runstatus.notification", params: {} }));

    expect(frames).toHaveLength(1);
    expect((frames[0] as { question_id: string }).question_id).toBe("q-7");

    unsub();
    expect(es.closed).toBe(true);
    await new Promise<void>((r) => setTimeout(r, 0));
    const calls = fetchMock.mock.calls as [string, RequestInit][];
    const subCall = calls.find((c) => {
      const b = JSON.parse(c[1].body as string) as { method: string };
      return b.method === "runstatus.questions.subscribe";
    });
    const unsubCall = calls.find((c) => {
      const b = JSON.parse(c[1].body as string) as { method: string };
      return b.method === "runstatus.questions.unsubscribe";
    });
    expect(subCall).toBeDefined();
    expect(unsubCall).toBeDefined();
  });

  it("answerQuestion posts the question_id and answers map", async () => {
    fetchMock.mockResolvedValueOnce(rpcOk({ ok: true }));
    const src = new LiveSource("/");
    const res = await src.answerQuestion("q-7", { Ship: "Yes" });
    expect(res.ok).toBe(true);
    const body = JSON.parse(
      (fetchMock.mock.calls[0] as [string, RequestInit])[1].body as string
    ) as { method: string; params: { question_id: string; answers: Record<string, unknown> } };
    expect(body.method).toBe("runstatus.session.answer_question");
    expect(body.params.question_id).toBe("q-7");
    expect(body.params.answers).toEqual({ Ship: "Yes" });
  });
});
