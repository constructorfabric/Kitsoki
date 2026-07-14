/**
 * Unit tests for src/stores/run.ts
 */

import { describe, it, expect, beforeEach, vi } from "vitest";
import { setActivePinia, createPinia } from "pinia";
import { useRunStore } from "../../src/stores/run.js";
import { SnapshotSource } from "../../src/data/snapshot-source.js";
import type { Snapshot, TraceEvent, TurnResult } from "../../src/types.js";
import type { DataSource } from "../../src/data/source.js";
import { TurnCancelledError } from "../../src/data/live-source.js";

// ---- Write-RPC stub helpers ------------------------------------------------
// The store's read path doesn't touch the write RPCs; these throwing stubs let
// the inline DataSource literals satisfy the full interface without pulling in
// a live transport.
const writeStubs = {
  view: () => Promise.reject(new Error("not stubbed")) as Promise<TurnResult>,
  submit: () =>
    Promise.reject(new Error("not stubbed")) as Promise<TurnResult>,
  sendTurn: () =>
    Promise.reject(new Error("not stubbed")) as Promise<TurnResult>,
  continueTurn: () =>
    Promise.reject(new Error("not stubbed")) as Promise<TurnResult>,
  driveOperation: () =>
    Promise.reject(new Error("not stubbed")) as Promise<TurnResult>,
  offpath: () =>
    Promise.reject(new Error("not stubbed")) as Promise<{ answer: string }>,
};

// ---- Fixture ---------------------------------------------------------------

const SNAPSHOT: Snapshot = {
  session: {
    session_id: "sess-1",
    app_id: "app-1",
    current_state: "root/review",
    turn: 3,
    started_at: "2026-01-01T00:00:00Z",
    terminal: false,
  },
  app: {
    id: "app-1",
    name: "Test App",
    root: "root",
    states: {
      "root/review": { description: "Reviewing" },
      "root/done": { description: "Done" },
    },
  },
  mermaid: {
    source: "flowchart LR\n  root_review --> root_done",
    node_map: {
      root_review: { kind: "state", ref: "root/review" },
      root_done: { kind: "state", ref: "root/done" },
      effect_0: { kind: "effect", ref: "root/review:0" },
      transition_0: { kind: "transition", ref: "root/review>root/done" },
    },
  },
  events: [
    {
      time: "2026-01-01T00:00:01Z",
      level: "info",
      msg: "TurnStarted",
      session_id: "sess-1",
      turn: 1,
      state_path: "root/review",
      attrs: {},
    },
    {
      time: "2026-01-01T00:00:02Z",
      level: "info",
      msg: "LLMCalled",
      session_id: "sess-1",
      turn: 2,
      state_path: "root/review",
      attrs: { tokens: 10 },
    },
    {
      time: "2026-01-01T00:00:03Z",
      level: "info",
      msg: "TransitionApplied",
      session_id: "sess-1",
      turn: 3,
      state_path: "root/done",
      attrs: {},
    },
  ],
};

// ---- Tests -----------------------------------------------------------------

beforeEach(() => {
  setActivePinia(createPinia());
});

describe("useRunStore — hydrate with SnapshotSource", () => {
  it("populates appDef, mermaid, events, currentStatePath after hydration", async () => {
    const store = useRunStore();
    const src = new SnapshotSource(SNAPSHOT);

    expect(store.loading).toBe(false);
    await store.hydrate(src, "sess-1");

    expect(store.appDef?.id).toBe("app-1");
    expect(store.mermaid?.source).toContain("flowchart LR");
    expect(store.events).toHaveLength(3);
    expect(store.currentStatePath).toBe("root/review");
    expect(store.terminal).toBe(false);
    expect(store.loading).toBe(false);
  });

  it("sets loading=true during hydration and false after", async () => {
    const store = useRunStore();
    const loadingStates: boolean[] = [];

    // Spy: capture loading state asynchronously via a slow source.
    let resolveGetSession!: (v: unknown) => void;
    const slowSource: DataSource = {
      getSession: () =>
        new Promise((resolve) => {
          resolveGetSession = resolve;
        }) as never,
      getApp: () => Promise.resolve(SNAPSHOT.app),
      getMermaid: () => Promise.resolve(SNAPSHOT.mermaid),
      getTrace: () =>
        Promise.resolve({ events: SNAPSHOT.events, last_turn: 3 }),
      listSessions: () => Promise.resolve([SNAPSHOT.session]),
      subscribe: () => () => undefined,
      ...writeStubs,
    };

    const hydratePromise = store.hydrate(slowSource, "sess-1");
    loadingStates.push(store.loading); // should be true

    resolveGetSession(SNAPSHOT.session);
    await hydratePromise;
    loadingStates.push(store.loading); // should be false

    expect(loadingStates[0]).toBe(true);
    expect(loadingStates[1]).toBe(false);
  });
});

describe("useRunStore — setHighlightedStatePaths", () => {
  it("sets highlightedStatePaths and bumps highlightTick", async () => {
    const store = useRunStore();
    await store.hydrate(new SnapshotSource(SNAPSHOT), "sess-1");

    expect(store.highlightedStatePaths).toEqual([]);
    const tick0 = store.highlightTick;

    store.setHighlightedStatePaths(["root/review", "root/done"]);
    expect(store.highlightedStatePaths).toEqual(["root/review", "root/done"]);
    expect(store.highlightTick).toBe(tick0 + 1);
  });

  it("clears highlightedStatePaths when called with empty array", async () => {
    const store = useRunStore();
    await store.hydrate(new SnapshotSource(SNAPSHOT), "sess-1");

    store.setHighlightedStatePaths(["root/review"]);
    store.setHighlightedStatePaths([]);
    expect(store.highlightedStatePaths).toEqual([]);
  });

  it("bumps highlightTick each call (re-clicking same room scrolls again)", async () => {
    const store = useRunStore();
    await store.hydrate(new SnapshotSource(SNAPSHOT), "sess-1");

    const tick0 = store.highlightTick;
    store.setHighlightedStatePaths(["root/review"]);
    store.setHighlightedStatePaths(["root/review"]);
    expect(store.highlightTick).toBe(tick0 + 2);
  });
});

describe("useRunStore — selectEvent", () => {
  it("sets selectedEventIndex", async () => {
    const store = useRunStore();
    await store.hydrate(new SnapshotSource(SNAPSHOT), "sess-1");

    store.selectEvent(2);
    expect(store.selectedEventIndex).toBe(2);

    store.selectEvent(0);
    expect(store.selectedEventIndex).toBe(0);
  });
});

describe("useRunStore — live event appending", () => {
  it("appends events and updates currentStatePath from live subscription", async () => {
    let capturedCallback: ((e: TraceEvent) => void) | null = null;

    const liveSource: DataSource = {
      listSessions: () => Promise.resolve([SNAPSHOT.session]),
      getSession: () => Promise.resolve(SNAPSHOT.session),
      getApp: () => Promise.resolve(SNAPSHOT.app),
      getMermaid: () => Promise.resolve(SNAPSHOT.mermaid),
      getTrace: () =>
        Promise.resolve({ events: SNAPSHOT.events.slice(0, 1), last_turn: 1 }),
      subscribe: (_sessionId, onEvent) => {
        capturedCallback = onEvent;
        return () => undefined;
      },
      ...writeStubs,
    };

    const store = useRunStore();
    await store.hydrate(liveSource, "sess-1");

    expect(store.events).toHaveLength(1);

    // Simulate a live event arriving.
    const newEvent: TraceEvent = {
      time: new Date().toISOString(),
      level: "info",
      msg: "TurnStarted",
      session_id: "sess-1",
      turn: 4,
      state_path: "root/done",
      attrs: {},
    };

    expect(capturedCallback).not.toBeNull();
    capturedCallback!(newEvent);

    expect(store.events).toHaveLength(2);
    expect(store.events[1]!.turn).toBe(4);
    expect(store.currentStatePath).toBe("root/done");
  });

  it("ignores replayed live events that are already present from hydration", async () => {
    let capturedCallback: ((e: TraceEvent) => void) | null = null;
    const initial = SNAPSHOT.events[0]!;

    const liveSource: DataSource = {
      listSessions: () => Promise.resolve([SNAPSHOT.session]),
      getSession: () => Promise.resolve(SNAPSHOT.session),
      getApp: () => Promise.resolve(SNAPSHOT.app),
      getMermaid: () => Promise.resolve(SNAPSHOT.mermaid),
      getTrace: () => Promise.resolve({ events: [initial], last_turn: 1 }),
      subscribe: (_sessionId, onEvent) => {
        capturedCallback = onEvent;
        return () => undefined;
      },
      ...writeStubs,
    };

    const store = useRunStore();
    await store.hydrate(liveSource, "sess-1");

    capturedCallback!({ ...initial, attrs: { ...initial.attrs } });

    expect(store.events).toHaveLength(1);
  });

  it("keeps distinct events that share a turn", async () => {
    let capturedCallback: ((e: TraceEvent) => void) | null = null;
    const initial = SNAPSHOT.events[0]!;
    const sameTurnDifferentEvent: TraceEvent = {
      ...initial,
      time: "2026-01-01T00:00:01.500Z",
      msg: "machine.intent_accepted",
      attrs: { intent: "continue" },
    };

    const liveSource: DataSource = {
      listSessions: () => Promise.resolve([SNAPSHOT.session]),
      getSession: () => Promise.resolve(SNAPSHOT.session),
      getApp: () => Promise.resolve(SNAPSHOT.app),
      getMermaid: () => Promise.resolve(SNAPSHOT.mermaid),
      getTrace: () => Promise.resolve({ events: [initial], last_turn: 1 }),
      subscribe: (_sessionId, onEvent) => {
        capturedCallback = onEvent;
        return () => undefined;
      },
      ...writeStubs,
    };

    const store = useRunStore();
    await store.hydrate(liveSource, "sess-1");

    capturedCallback!(sameTurnDifferentEvent);

    expect(store.events).toHaveLength(2);
    expect(store.events[1]!.msg).toBe("machine.intent_accepted");
  });

  it("teardown calls the unsubscribe function", async () => {
    let unsubCalled = false;
    const src: DataSource = {
      listSessions: () => Promise.resolve([SNAPSHOT.session]),
      getSession: () => Promise.resolve(SNAPSHOT.session),
      getApp: () => Promise.resolve(SNAPSHOT.app),
      getMermaid: () => Promise.resolve(SNAPSHOT.mermaid),
      getTrace: () =>
        Promise.resolve({ events: [], last_turn: 0 }),
      subscribe: () => {
        return () => { unsubCalled = true; };
      },
      ...writeStubs,
    };

    const store = useRunStore();
    await store.hydrate(src, "sess-1");
    store.teardown();
    expect(unsubCalled).toBe(true);
  });

  it("marks terminal when an operation waiting event lands over the live stream", async () => {
    let capturedCallback: ((e: TraceEvent) => void) | null = null;
    const liveSource: DataSource = {
      listSessions: () => Promise.resolve([SNAPSHOT.session]),
      getSession: () => Promise.resolve({ ...SNAPSHOT.session, terminal: false }),
      getApp: () => Promise.resolve(SNAPSHOT.app),
      getMermaid: () => Promise.resolve(SNAPSHOT.mermaid),
      getTrace: () => Promise.resolve({ events: [], last_turn: 0 }),
      subscribe: (_sessionId, onEvent) => {
        capturedCallback = onEvent;
        return () => undefined;
      },
      ...writeStubs,
    };

    const store = useRunStore();
    await store.hydrate(liveSource, "sess-1");
    expect(store.terminal).toBe(false);

    capturedCallback!({
      time: new Date().toISOString(),
      level: "info",
      msg: "operation.waiting",
      session_id: "sess-1",
      turn: 5,
      state_path: "testing",
      attrs: {
        operation_id: "bugfix_full",
        status: "waiting",
        terminal_state: "__exit__needs-human",
        stop_reason: "needs-human",
      },
    });

    expect(store.terminal).toBe(true);
    expect(store.currentStatePath).toBe("__exit__needs-human");
  });
});

// ---- currentStatePath bug fix ---------------------------------------------

describe("useRunStore — currentStatePath prefers machine.state_entered", () => {
  function liveSourceCapturing(onCap: (cb: (e: TraceEvent) => void) => void): DataSource {
    return {
      listSessions: () => Promise.resolve([SNAPSHOT.session]),
      getSession: () => Promise.resolve(SNAPSHOT.session),
      getApp: () => Promise.resolve(SNAPSHOT.app),
      getMermaid: () => Promise.resolve(SNAPSHOT.mermaid),
      getTrace: () => Promise.resolve({ events: [], last_turn: 0 }),
      subscribe: (_sid, onEvent) => {
        onCap(onEvent);
        return () => undefined;
      },
      ...writeStubs,
    };
  }

  function ev(msg: string, statePath: string, turn: number): TraceEvent {
    return {
      time: new Date().toISOString(),
      level: "info",
      msg,
      session_id: "sess-1",
      turn,
      state_path: statePath,
      attrs: {},
    };
  }

  it("a later turn.end stamped with the STARTING state does not rewind the landed state", async () => {
    let cb!: (e: TraceEvent) => void;
    const store = useRunStore();
    await store.hydrate(
      liveSourceCapturing((c) => { cb = c; }),
      "sess-1"
    );

    // The turn started in root/review, entered root/done, then turn.end is
    // stamped with the STARTING state (root/review). The landed state must win.
    cb(ev("machine.state_entered", "root/done", 4));
    expect(store.currentStatePath).toBe("root/done");

    cb(ev("turn.end", "root/review", 4));
    expect(store.currentStatePath).toBe("root/done");
  });

  it("falls back to a raw state_path until the first state_entered is seen", async () => {
    let cb!: (e: TraceEvent) => void;
    const store = useRunStore();
    await store.hydrate(
      liveSourceCapturing((c) => { cb = c; }),
      "sess-1"
    );

    // Before any state_entered, a bare event's state_path seeds the UI.
    cb(ev("turn.start", "root/review", 1));
    expect(store.currentStatePath).toBe("root/review");

    // Once state_entered arrives it becomes authoritative.
    cb(ev("machine.state_entered", "root/done", 2));
    expect(store.currentStatePath).toBe("root/done");
  });
});

// ---- write-side actions ----------------------------------------------------

describe("useRunStore — write-side actions", () => {
  function turnResult(over: Partial<TurnResult> = {}): TurnResult {
    return {
      mode: "transitioned",
      state: "idle",
      view: "Welcome to PRD discovery",
      typed_view: { Source: "", Elements: [{ Kind: "heading", Source: "PRD discovery" }] },
      allowed_intents: ["start", "discuss"],
      intents: [
        { name: "start", has_slots: false },
        { name: "discuss", text_slot: "message", has_slots: true },
      ],
      turn_number: 1,
      ...over,
    };
  }

  function writeSource(over: Partial<DataSource> = {}): DataSource {
    return {
      listSessions: () => Promise.resolve([SNAPSHOT.session]),
      getSession: () => Promise.resolve(SNAPSHOT.session),
      getApp: () => Promise.resolve(SNAPSHOT.app),
      getMermaid: () => Promise.resolve(SNAPSHOT.mermaid),
      getTrace: () => Promise.resolve({ events: [], last_turn: 0 }),
      subscribe: () => () => undefined,
      ...writeStubs,
      ...over,
    };
  }

  it("loadInitialView seeds currentView, allowedIntents, currentStatePath, and an opening agent entry", async () => {
    const result = turnResult();
    const src = writeSource({ view: () => Promise.resolve(result) });

    const store = useRunStore();
    await store.loadInitialView(src, "sess-1");

    expect(store.currentView).toEqual(result);
    expect(store.currentStatePath).toBe("idle");
    expect(store.allowedIntents.map((i) => i.name)).toEqual(["start", "discuss"]);
    expect(store.transcript).toHaveLength(1);
    expect(store.transcript[0]!.role).toBe("agent");
    expect(store.transcript[0]!.text).toBe("Welcome to PRD discovery");
    expect(store.transcript[0]!.typedView?.Elements?.[0]!.Kind).toBe("heading");
    expect(store.terminal).toBe(false);
  });

  it("submitIntent pushes a user entry, calls submit, applies the result, and pushes an agent entry", async () => {
    let captured: { intent: string; slots?: Record<string, unknown> } | null = null;
    const next = turnResult({ state: "idle", view: "Discovery in progress", turn_number: 2 });
    const src = writeSource({
      submit: (_sid, intent, slots) => {
        captured = { intent, slots };
        return Promise.resolve(next);
      },
    });

    const store = useRunStore();
    const out = await store.submitIntent(src, "sess-1", "discuss", { message: "I want a CLI for X" });

    expect(captured).toEqual({ intent: "discuss", slots: { message: "I want a CLI for X" } });
    expect(out).toEqual(next);
    expect(store.transcript).toHaveLength(2);
    expect(store.transcript[0]).toMatchObject({ role: "user", text: "I want a CLI for X" });
    expect(store.transcript[1]).toMatchObject({ role: "agent", text: "Discovery in progress" });
    expect(store.currentView).toEqual(next);
    expect(store.currentStatePath).toBe("idle");
  });

  it("submitIntent with a no-slot intent labels the user entry with the intent name", async () => {
    const src = writeSource({ submit: () => Promise.resolve(turnResult({ state: "clarifying" })) });
    const store = useRunStore();
    await store.submitIntent(src, "sess-1", "start", {});
    expect(store.transcript[0]).toMatchObject({ role: "user", text: "start" });
    expect(store.currentStatePath).toBe("clarifying");
  });

  it("submitIntent sets terminal on mode=completed", async () => {
    const src = writeSource({
      submit: () => Promise.resolve(turnResult({ mode: "completed", state: "__exit__done" })),
    });
    const store = useRunStore();
    await store.submitIntent(src, "sess-1", "accept", {});
    expect(store.terminal).toBe(true);
    expect(store.currentStatePath).toBe("__exit__done");
  });

  it("submitIntent renders a rejection's error_message as the agent entry when no view is present", async () => {
    const src = writeSource({
      submit: () =>
        Promise.resolve(
          turnResult({ mode: "rejected", view: undefined, error_message: "guard failed: not ready" })
        ),
    });
    const store = useRunStore();
    await store.submitIntent(src, "sess-1", "confirm", {});
    expect(store.transcript[1]).toMatchObject({ role: "agent", text: "guard failed: not ready" });
  });

  it("submitIntent renders a clarify's slot prompts when no view is present", async () => {
    const src = writeSource({
      submit: () =>
        Promise.resolve(
          turnResult({
            mode: "clarify",
            view: undefined,
            slots_needed: [{ Name: "n", Prompt: "How many?" }],
          })
        ),
    });
    const store = useRunStore();
    await store.submitIntent(src, "sess-1", "answer", {});
    expect(store.transcript[1]).toMatchObject({ role: "agent", text: "How many?" });
  });

  // ---- off-ramp ("offpath") turn ----
  // An unroutable free-text utterance fires the agent off-ramp: a voiced
  // converse answer that does NOT advance state. The result is mode "offpath"
  // carrying the converse answer as `view`, the UN-advanced state, and the
  // room's SAME menu echoed unchanged. The store must (a) mark the agent bubble
  // isOffRamp so it can render distinctly, and (b) keep state + the menu intact.
  it("sendText flags an offpath turn's agent bubble isOffRamp and keeps the menu unchanged", async () => {
    const src = writeSource({
      sendTurn: () =>
        Promise.resolve(
          turnResult({
            mode: "offpath",
            // Off-ramp does NOT advance: same resting room, same menu echoed.
            state: "idle",
            view: "Kitsoki is a deterministic state-machine runtime for agents.",
            allowed_intents: ["start", "discuss"],
            intents: [
              { name: "start", has_slots: false },
              { name: "discuss", text_slot: "message", has_slots: true },
            ],
          })
        ),
    });
    const store = useRunStore();
    await store.sendText(src, "sess-1", "what even is kitsoki?", "discuss");

    const agent = store.transcript[1]!;
    expect(agent).toMatchObject({
      role: "agent",
      text: "Kitsoki is a deterministic state-machine runtime for agents.",
      isOffRamp: true,
    });
    // State is unchanged (no advance) and not terminal.
    expect(store.currentStatePath).toBe("idle");
    expect(store.terminal).toBe(false);
    // The room's menu persists, echoed unchanged.
    expect(store.allowedIntents.map((i) => i.name)).toEqual(["start", "discuss"]);
  });

  // A normal (transitioned / rejected) agent bubble must NOT be flagged
  // isOffRamp — the off-ramp treatment is reserved for mode "offpath".
  it("does not flag a normal transitioned turn as isOffRamp", async () => {
    const src = writeSource({ sendTurn: () => Promise.resolve(turnResult({ view: "Onward." })) });
    const store = useRunStore();
    await store.sendText(src, "sess-1", "ok", "discuss");
    expect(store.transcript[1]!.isOffRamp).toBeUndefined();
  });

  // ---- rewindRoute ----
  // The route-receipt chip's reroute affordance drives store.rewindRoute, which
  // reverses one CRR decision and applies the re-dispatched turn. It must call
  // the source's rewindRoute with the decision id, push a "rewound" marker, and
  // thread the re-dispatched turn through applyTurnResult.
  it("rewindRoute calls the source with the decision id, applies the re-dispatched turn", async () => {
    let captured: { decisionId: string; newClass?: string } | null = null;
    const redispatched = turnResult({ state: "help-lane", view: "Here's how that works." });
    const src = writeSource() as DataSource & {
      rewindRoute: (sid: string, decisionId: string, newClass?: string) => Promise<TurnResult>;
    };
    src.rewindRoute = (_sid, decisionId, newClass) => {
      captured = { decisionId, newClass };
      return Promise.resolve(redispatched);
    };

    const store = useRunStore();
    const out = await store.rewindRoute(src, "sess-1", "sess-1:3");

    expect(captured).toEqual({ decisionId: "sess-1:3", newClass: undefined });
    expect(out).toEqual(redispatched);
    // A "rewound" marker precedes the re-dispatched agent reply.
    expect(store.transcript[0]).toMatchObject({ role: "user" });
    expect(store.transcript[0]!.text).toContain("sess-1:3");
    expect(store.transcript[1]).toMatchObject({ role: "agent", text: "Here's how that works." });
    expect(store.currentStatePath).toBe("help-lane");
  });

  it("rewindRoute is a no-op on a source without the optional method", async () => {
    const src = writeSource(); // no rewindRoute
    const store = useRunStore();
    const out = await store.rewindRoute(src, "sess-1", "sess-1:3");
    expect(out).toBeUndefined();
    expect(store.transcript).toHaveLength(0);
  });

  it("rewindRoute propagates the engine's rejection (e.g. intent-class not supported)", async () => {
    const src = writeSource() as DataSource & {
      rewindRoute: () => Promise<TurnResult>;
    };
    src.rewindRoute = () =>
      Promise.reject(new Error("class=intent rewind requires IntentAccepted recovery; not yet implemented"));
    const store = useRunStore();
    await expect(store.rewindRoute(src, "sess-1", "sess-1:7", "intent")).rejects.toThrow(
      /not yet implemented/
    );
  });

  // ---- sendRoutingFeedback (WS-C C4 web control) ----
  // The routing chip's thumbs-up/down control drives store.sendRoutingFeedback,
  // which posts the entry's own recovered provenance (state/intent/tier) + its
  // phrase to the source, then marks the master transcript entry so the chip
  // hides in favour of a "recorded" indicator — without advancing the turn (no
  // TurnResult is applied).
  it("sendRoutingFeedback posts the entry's routing provenance and marks it given", async () => {
    let captured: unknown = null;
    const src = writeSource({
      sendTurn: () => Promise.resolve(turnResult({ view: "Got it." })),
    }) as DataSource & {
      routingFeedback: (
        sid: string,
        feedback: { state: string; intent: string; phrase: string; tier: string; verdict: string }
      ) => Promise<void>;
    };
    src.routingFeedback = (_sid, feedback) => {
      captured = feedback;
      return Promise.resolve();
    };

    const store = useRunStore();
    await store.sendText(src, "sess-1", "fix the crash on login", "discuss");
    // sendText doesn't stamp routing (no trace events wired in this fixture),
    // so drive the entry directly the way chatEntries would enrich it.
    const entry = store.transcript[0]!;
    entry.turn = 1;
    entry.routing = { routedBy: "semantic", intent: "go_bugfix", statePath: "hub" };

    await store.sendRoutingFeedback(src, "sess-1", entry, "down");

    expect(captured).toEqual({
      state: "hub",
      intent: "go_bugfix",
      phrase: "fix the crash on login",
      tier: "semantic",
      verdict: "down",
    });
    expect(store.transcript[0]!.feedbackGiven).toBe("down");
  });

  it("sendRoutingFeedback is a no-op on a source without the optional method", async () => {
    const src = writeSource({
      sendTurn: () => Promise.resolve(turnResult({ view: "Got it." })),
    }); // no routingFeedback
    const store = useRunStore();
    await store.sendText(src, "sess-1", "hi", "discuss");
    const entry = store.transcript[0]!;
    entry.turn = 1;
    await store.sendRoutingFeedback(src, "sess-1", entry, "up");
    expect(store.transcript[0]!.feedbackGiven).toBeUndefined();
  });

  it("sendText pushes the raw text as the user entry and applies the result", async () => {
    let capturedInput = "";
    const src = writeSource({
      sendTurn: (_sid, input) => {
        capturedInput = input;
        return Promise.resolve(turnResult({ view: "Got it." }));
      },
    });
    const store = useRunStore();
    await store.sendText(src, "sess-1", "build me a thing", "discuss");
    expect(capturedInput).toBe("build me a thing");
    expect(store.transcript[0]).toMatchObject({ role: "user", text: "build me a thing" });
    expect(store.transcript[1]).toMatchObject({ role: "agent", text: "Got it." });
  });

  it("driveOperation backfills all driven turns and applies the returned view", async () => {
    const drivenEvents: TraceEvent[] = [
      {
        time: "2026-01-01T00:00:04Z",
        level: "info",
        msg: "operation.run_started",
        session_id: "sess-1",
        turn: 4,
        state_path: "root/review",
        attrs: {
          operation_id: "bf__capsule_demo",
          policy_id: "bf__capsule_demo",
          title: "Capsule bugfix",
          status: "running",
          phase: "run_regression",
        },
      },
      {
        time: "2026-01-01T00:00:05Z",
        level: "info",
        msg: "operation.completed",
        session_id: "sess-1",
        turn: 5,
        state_path: "__exit__done",
        attrs: {
          operation_id: "bf__capsule_demo",
          policy_id: "bf__capsule_demo",
          title: "Capsule bugfix",
          status: "completed",
          terminal_state: "__exit__done",
          terminal_artifact: "artifacts/qa-report.md",
          terminal_artifact_handle: "qa-report#abc123",
        },
      },
    ];
    const getTrace = vi.fn(
      (_sid: string, cursor?: { since_turn?: number }) =>
        Promise.resolve(
          cursor?.since_turn === undefined
            ? { events: SNAPSHOT.events, last_turn: 3 }
            : {
                events: drivenEvents.filter((event) => event.turn >= (cursor.since_turn ?? 0)),
                last_turn: 5,
              }
        )
    );
    const driveOperation = vi.fn(() =>
      Promise.resolve(
        turnResult({
          mode: "completed",
          state: "__exit__done",
          view: "Operation complete",
          turn_number: 5,
        })
      )
    );
    const src = writeSource({ getTrace, driveOperation });

    const store = useRunStore();
    await store.hydrate(src, "sess-1");
    await store.driveOperation(src, "sess-1");

    expect(driveOperation).toHaveBeenCalledWith("sess-1");
    expect(getTrace).toHaveBeenLastCalledWith("sess-1", { since_turn: 4 });
    expect(store.events.some((event) => event.msg === "operation.run_started")).toBe(true);
    expect(store.events.some((event) => event.msg === "operation.completed")).toBe(true);
    expect(store.operationRun).toMatchObject({
      status: "completed",
      phase: "run_regression",
      terminalState: "__exit__done",
      terminalArtifact: "artifacts/qa-report.md",
      terminalArtifactHandle: "qa-report#abc123",
    });
    expect(store.transcript[0]).toMatchObject({ role: "user", text: "Drive operation" });
    expect(store.transcript[1]).toMatchObject({ role: "agent", text: "Operation complete" });
    expect(store.currentStatePath).toBe("__exit__done");
    expect(store.terminal).toBe(true);
  });

  it("driveOperation narrates the operation drive stop reason", async () => {
    const driveOperation = vi.fn(() =>
      Promise.resolve(
        turnResult({
          state: "review",
          view: "Review checkpoint",
          turn_number: 2,
          operation_drive: {
            turns: 1,
            stop_reason: "no-driver-intent",
            last_intent: "accept",
          },
        })
      )
    );
    const src = writeSource({ driveOperation });

    const store = useRunStore();
    await store.driveOperation(src, "sess-1");

    expect(store.transcript).toHaveLength(3);
    expect(store.transcript[0]).toMatchObject({ role: "user", text: "Drive operation" });
    expect(store.transcript[1]).toMatchObject({ role: "agent", text: "Review checkpoint" });
    expect(store.transcript[2]).toMatchObject({
      role: "narration",
      text: "Drove 1 turn via accept; stopped at a checkpoint.",
    });
  });

  // ---- live turn-stream feed ordering ----
  // The streaming bubble must show thinking and tool calls in ARRIVAL order
  // (a thought stays ABOVE the tools that follow it, 🧠-style like the TUI).
  // Regression: the old store split deltas and tools into two buckets, so the
  // view rendered every tool above the thinking and "pushed it to the bottom".
  it("sendText over a LiveSource keeps the thinking/tool feed in arrival order", async () => {
    let resolveTurn!: (r: TurnResult) => void;
    const src = writeSource() as DataSource & {
      turnStream: (
        sid: string,
        method: string,
        params: unknown,
        onEvent: (ev: { type: string; text?: string; tool?: string; preview?: string }) => void
      ) => Promise<TurnResult>;
    };
    src.turnStream = (_sid, _method, _params, onEvent) => {
      onEvent({ type: "delta", text: "Reading the failing test first." });
      onEvent({ type: "tool", tool: "Read", preview: "bar_test.go" });
      onEvent({ type: "tool", tool: "Grep", preview: "func Bar" });
      onEvent({ type: "delta", text: "The off-by-one is in the loop bound." });
      onEvent({ type: "tool", tool: "Edit", preview: "bar.go" });
      return new Promise<TurnResult>((resolve) => {
        resolveTurn = resolve;
      });
    };

    const store = useRunStore();
    const turn = store.sendText(src, "sess-1", "fix it");

    // Mid-flight: one ordered feed, thoughts interleaved with their tools.
    expect(
      store.pendingStream.map((it) =>
        it.kind === "thinking" ? `think:${it.text}` : `tool:${it.tool}`
      )
    ).toEqual([
      "think:Reading the failing test first.",
      "tool:Read",
      "tool:Grep",
      "think:The off-by-one is in the loop bound.",
      "tool:Edit",
    ]);

    resolveTurn(turnResult({ view: "static view" }));
    await turn;

    // Live feed cleared, but the turn's activity SURVIVES on the transcript
    // entry (rendered collapsed in the agent bubble) — it must not vanish
    // when the final view renders. The bubble text is the final view.
    expect(store.pendingStream).toEqual([]);
    expect(store.transcript[1]!.text).toBe("static view");
    expect(
      store.transcript[1]!.stream!.map((it) =>
        it.kind === "thinking" ? `think:${it.text}` : `tool:${it.tool}`
      )
    ).toEqual([
      "think:Reading the failing test first.",
      "tool:Read",
      "tool:Grep",
      "think:The off-by-one is in the loop bound.",
      "tool:Edit",
    ]);
  });

  it("sendText rides the viewed deck scene as a current_scene supplement slot", async () => {
    let seenParams: { input?: string; slots?: Record<string, unknown> } | undefined;
    const src = writeSource() as DataSource & {
      turnStream: (
        sid: string,
        method: string,
        params: { input?: string; slots?: Record<string, unknown> },
        onEvent: (ev: unknown) => void
      ) => Promise<TurnResult>;
    };
    src.turnStream = (_sid, _method, params) => {
      seenParams = params;
      return Promise.resolve(turnResult({ view: "ok" }));
    };

    const store = useRunStore();
    // The live deck reports the operator is on scene 9.
    store.setEmbedView({ producer: "slidey", scope: "9", label: "Cat Wrangling" });

    await store.sendText(src, "sess-1", "make the title bolder");
    expect(seenParams?.slots).toEqual({ current_scene: "9" });
  });

  it("sendText omits slots when no deck is being viewed", async () => {
    let seenParams: { input?: string; slots?: Record<string, unknown> } | undefined;
    const src = writeSource() as DataSource & {
      turnStream: (
        sid: string,
        method: string,
        params: { input?: string; slots?: Record<string, unknown> },
        onEvent: (ev: unknown) => void
      ) => Promise<TurnResult>;
    };
    src.turnStream = (_sid, _method, params) => {
      seenParams = params;
      return Promise.resolve(turnResult({ view: "ok" }));
    };

    const store = useRunStore();
    await store.sendText(src, "sess-1", "hello");
    expect(seenParams?.slots).toBeUndefined();
  });

  it("sendText over a LiveSource clears the feed and rejects with TurnCancelledError when the turn is cancelled", async () => {
    const src = writeSource() as DataSource & { turnStream: unknown };
    (src as { turnStream: unknown }).turnStream = (
      _sid: string,
      _method: string,
      _params: unknown,
      onEvent: (ev: { type: string; text?: string; tool?: string; preview?: string }) => void
    ) => {
      onEvent({ type: "delta", text: "Working on it…" });
      // Operator hits Stop: the server emits a "cancelled" frame, which
      // LiveSource.turnStream surfaces as a rejected TurnCancelledError.
      return Promise.reject(new TurnCancelledError());
    };

    const store = useRunStore();
    await expect(store.sendText(src, "sess-1", "do the thing")).rejects.toBeInstanceOf(
      TurnCancelledError
    );

    // The live bubble is cleared (no lingering "thinking" spinner) and the
    // user's message survives — no agent reply was applied.
    expect(store.pendingStream).toEqual([]);
    expect(store.transcript).toHaveLength(1);
    expect(store.transcript[0]!.role).toBe("user");
    expect(store.transcript[0]!.text).toBe("do the thing");
  });

  it("shows streamed routing on the user bubble before the turn result completes", async () => {
    let resolveTurn!: (r: TurnResult) => void;
    const src = writeSource() as DataSource & { turnStream: unknown };
    (src as { turnStream: unknown }).turnStream = (
      _sid: string,
      _method: string,
      _params: unknown,
      onEvent: (ev: {
        type: string;
        turn?: number;
        intent?: string;
        routed_by?: string;
        match_type?: string;
        confidence?: number;
      }) => void
    ) => {
      onEvent({
        type: "routing",
        turn: 4,
        intent: "core.work",
        routed_by: "fallback",
        match_type: "free_text",
      });
      return new Promise<TurnResult>((resolve) => {
        resolveTurn = resolve;
      });
    };

    const store = useRunStore();
    const turn = store.sendText(src, "sess-1", "do this ad hoc thing");

    expect(store.chatEntries[0]!.routing).toEqual({
      routedBy: "fallback",
      matchType: "free_text",
      confidence: undefined,
      intent: "core.work",
    });

    resolveTurn(turnResult({ turn_number: 4, view: "Workbench ready" }));
    await turn;
  });

  it("merges consecutive deltas into one thinking item (chunks inline, thoughts as paragraphs)", async () => {
    let resolveTurn!: (r: TurnResult) => void;
    const src = writeSource() as DataSource & { turnStream: unknown };
    (src as { turnStream: unknown }).turnStream = (
      _sid: string,
      _method: string,
      _params: unknown,
      onEvent: (ev: { type: string; text?: string; tool?: string }) => void
    ) => {
      // Chunked sender: fragments with trailing spaces reassemble inline.
      onEvent({ type: "delta", text: "Hello " });
      onEvent({ type: "delta", text: "world." });
      // A complete thought after a complete thought becomes a new paragraph.
      onEvent({ type: "delta", text: "Second thought." });
      onEvent({ type: "tool", tool: "Bash" });
      // A tool frame ends the run: the next thought is its own item.
      onEvent({ type: "delta", text: "After the tool." });
      onEvent({ type: "tool", tool: "Read" });
      return new Promise<TurnResult>((resolve) => {
        resolveTurn = resolve;
      });
    };

    const store = useRunStore();
    const turn = store.sendText(src, "sess-1", "go");

    expect(store.pendingStream).toEqual([
      { kind: "thinking", text: "Hello world.\n\nSecond thought." },
      { kind: "tool", tool: "Bash", preview: "" },
      { kind: "thinking", text: "After the tool." },
      { kind: "tool", tool: "Read", preview: "" },
    ]);

    resolveTurn(turnResult({ view: "v" }));
    await turn;
    // Bubble text is the final view; the merged feed survives on the entry.
    expect(store.transcript[1]!.text).toBe("v");
    expect(store.transcript[1]!.stream).toEqual([
      { kind: "thinking", text: "Hello world.\n\nSecond thought." },
      { kind: "tool", tool: "Bash", preview: "" },
      { kind: "thinking", text: "After the tool." },
      { kind: "tool", tool: "Read", preview: "" },
    ]);
  });

  it("drops a trailing final narration delta from the preserved activity feed", async () => {
    const src = writeSource() as DataSource & { turnStream: unknown };
    (src as { turnStream: unknown }).turnStream = (
      _sid: string,
      _method: string,
      _params: unknown,
      onEvent: (ev: { type: string; text?: string; tool?: string; preview?: string }) => void
    ) => {
      onEvent({ type: "delta", text: "I will inspect the file first." });
      onEvent({ type: "tool", tool: "Read", preview: "app.go" });
      onEvent({ type: "delta", text: "Final answer from the backend." });
      return Promise.resolve(turnResult({ view: "Room view wins." }));
    };

    const store = useRunStore();
    await store.sendText(src, "sess-1", "go");

    expect(store.transcript[1]!.text).toBe("Room view wins.");
    expect(store.transcript[1]!.stream).toEqual([
      { kind: "thinking", text: "I will inspect the file first." },
      { kind: "tool", tool: "Read", preview: "app.go" },
    ]);
  });

  it("falls back to the streamed thinking as bubble text on a view-less turn", async () => {
    const src = writeSource() as DataSource & { turnStream: unknown };
    (src as { turnStream: unknown }).turnStream = (
      _sid: string,
      _method: string,
      _params: unknown,
      onEvent: (ev: { type: string; text?: string }) => void
    ) => {
      onEvent({ type: "delta", text: "Only narration this turn." });
      return Promise.resolve(turnResult({ view: undefined }));
    };
    const store = useRunStore();
    await store.sendText(src, "sess-1", "go");
    expect(store.transcript[1]!.text).toBe("Only narration this turn.");
    expect(store.transcript[1]!.stream).toBeUndefined();
  });

  it("backfills the completed streamed free-text turn so LLM routing chips render when live events are missed", async () => {
    const llmTurnStart = traceEvent({
      turn: 7,
      msg: "turn.start",
      attrs: { input: "do the ad hoc work", routed_by: "llm", match_type: "main-turn", confidence: 0.82 },
    });
    const transition = traceEvent({
      turn: 7,
      msg: "machine.transition",
      attrs: { intent: "workbench.ad_hoc" },
    });
    const getTrace = vi.fn().mockResolvedValue({
      events: [llmTurnStart, transition],
      last_turn: 7,
    });

    const src = writeSource({ getTrace }) as DataSource & { turnStream: unknown };
    (src as { turnStream: unknown }).turnStream = () =>
      Promise.resolve(turnResult({ turn_number: 7, view: "Workbench ready" }));

    const store = useRunStore();
    await store.sendText(src, "sess-1", "do the ad hoc work");

    expect(getTrace).toHaveBeenCalledWith("sess-1", { since_turn: 7 });
    expect(store.chatEntries[0]!.routing).toEqual({
      routedBy: "llm",
      matchType: "main-turn",
      confidence: 0.82,
      intent: "workbench.ad_hoc",
      statePath: "root/idle",
    });
  });

  // ---- session-switch isolation (bug: transcripts mixed across sessions) ----
  // When the operator switches from one session's chat to another, the store is
  // a singleton — hydrating the second session must drop the first session's
  // conversational state, or its transcript bubbles bleed into the new session.
  it("hydrate clears the prior session's transcript and view state", async () => {
    const first = turnResult({ state: "idle", view: "Session ONE opening" });
    const srcA = writeSource({ view: () => Promise.resolve(first) });

    const store = useRunStore();
    await store.hydrate(srcA, "sess-1");
    await store.loadInitialView(srcA, "sess-1");
    await store.submitIntent(
      writeSource({ submit: () => Promise.resolve(turnResult({ view: "ONE reply" })) }),
      "sess-1",
      "discuss",
      { message: "hello from one" }
    );
    expect(store.transcript.length).toBeGreaterThan(0);

    // Switch to a second session.
    const second = turnResult({ state: "idle", view: "Session TWO opening" });
    const srcB = writeSource({ view: () => Promise.resolve(second) });
    await store.hydrate(srcB, "sess-2");

    // The first session's bubbles must be gone before the new view seeds.
    expect(store.transcript).toEqual([]);
    expect(store.currentView).toBeNull();
    expect(store.selectedEventIndex).toBeNull();
    expect(store.highlightedStatePaths).toEqual([]);

    await store.loadInitialView(srcB, "sess-2");
    expect(store.transcript).toHaveLength(1);
    expect(store.transcript[0]).toMatchObject({ role: "agent", text: "Session TWO opening" });
    expect(store.transcript.some((e) => e.text.includes("ONE"))).toBe(false);
  });
});

// ---- chatEntries routing provenance ----------------------------------------
// chatEntries is the routing-chip's data source: it enriches each user turn
// with the routed_by/match_type/confidence off turn.start plus the resolved
// intent off the turn's first machine.transition. The raw transcript stays
// bare — surfaces MUST bind chatEntries to show the chip (see ChatSurface /
// InteractiveView, and chat-surface-routing.test.ts).
function traceEvent(over: Partial<TraceEvent>): TraceEvent {
  return {
    time: "2026-01-01T00:00:00Z",
    level: "info",
    msg: "",
    session_id: "sess-1",
    turn: 0,
    state_path: "root/idle",
    attrs: {},
    ...over,
  };
}

describe("run store — chatEntries routing provenance", () => {
  it("enriches a user turn with routing recovered from the event log", () => {
    const store = useRunStore();
    // A free-text user turn (tagged with its turn number, as sendText does).
    store.transcript = [{ role: "user", text: "commit my work", turn: 1 }];
    // The provenance the chip needs: tier/reason/confidence on turn.start, and
    // the resolved intent on the turn's FIRST machine.transition.
    store.events = [
      traceEvent({
        turn: 1,
        msg: "turn.start",
        attrs: { routed_by: "semantic", match_type: "leading-verb:commit", confidence: 0.95 },
      }),
      traceEvent({ turn: 1, msg: "machine.transition", attrs: { intent: "git.commit" } }),
    ];

    expect(store.chatEntries[0]!.routing).toEqual({
      routedBy: "semantic",
      matchType: "leading-verb:commit",
      confidence: 0.95,
      intent: "git.commit",
      statePath: "root/idle",
    });
    // The raw transcript is never mutated — only chatEntries carries routing,
    // which is exactly why a surface binding the raw transcript loses the chip.
    expect(store.transcript[0]!.routing).toBeUndefined();
  });

  it("leaves the user turn un-enriched until turn.start lands (chip stays hidden)", () => {
    const store = useRunStore();
    store.transcript = [{ role: "user", text: "commit my work", turn: 1 }];
    // Only the transition has landed — no turn.start yet, so no provenance.
    store.events = [traceEvent({ turn: 1, msg: "machine.transition", attrs: { intent: "git.commit" } })];
    expect(store.chatEntries[0]!.routing).toBeUndefined();
  });

  it("recovers provenance reactively when turn.start arrives a tick later (SSE settle)", async () => {
    const { nextTick } = await import("vue");
    const store = useRunStore();
    store.transcript = [{ role: "user", text: "commit my work", turn: 1 }];
    store.events = [];
    expect(store.chatEntries[0]!.routing).toBeUndefined();

    // Events stream in over SSE after the bubble; chatEntries is a computed so
    // the chip fills in without any imperative refresh.
    store.events = [
      traceEvent({ turn: 1, msg: "turn.start", attrs: { routed_by: "deterministic" } }),
      traceEvent({ turn: 1, msg: "machine.transition", attrs: { intent: "git.commit" } }),
    ];
    await nextTick();
    expect(store.chatEntries[0]!.routing).toMatchObject({
      routedBy: "deterministic",
      intent: "git.commit",
    });
  });
});

describe("useRunStore — SSE connection state", () => {
  // A DataSource whose subscribe captures the onConnectionChange callback so the
  // test can simulate the transport's stream-error / reopen liveness signals
  // without a real EventSource.
  function captureSource(): {
    source: DataSource;
    emit: (state: import("../../src/data/source.js").ConnectionState) => void;
  } {
    let onChange:
      | ((s: import("../../src/data/source.js").ConnectionState) => void)
      | undefined;
    const source: DataSource = {
      getSession: () => Promise.resolve(SNAPSHOT.session),
      getApp: () => Promise.resolve(SNAPSHOT.app),
      getMermaid: () => Promise.resolve(SNAPSHOT.mermaid),
      getTrace: () =>
        Promise.resolve({ events: SNAPSHOT.events, last_turn: 3 }),
      listSessions: () => Promise.resolve([SNAPSHOT.session]),
      subscribe: (_id, _onEvent, onConnectionChange) => {
        onChange = onConnectionChange;
        return () => undefined;
      },
      ...writeStubs,
    };
    return { source, emit: (s) => onChange?.(s) };
  }

  it("defaults to connected after hydration", async () => {
    const store = useRunStore();
    const { source } = captureSource();
    await store.hydrate(source, "sess-1");
    expect(store.connectionState).toBe("connected");
  });

  it("exposes 'reconnecting' when the stream drops, then 'connected' on reopen", async () => {
    const store = useRunStore();
    const { source, emit } = captureSource();
    await store.hydrate(source, "sess-1");

    emit("reconnecting");
    expect(store.connectionState).toBe("reconnecting");

    emit("connected");
    expect(store.connectionState).toBe("connected");
  });
});

describe("useRunStore — applyTurnResult threads the CRR route receipt", () => {
  it("carries context_route onto the agent bubble so the receipt chip renders", () => {
    const store = useRunStore();
    const result: TurnResult = {
      mode: "transitioned",
      state: "workbench",
      view: "Workbench ready.",
      turn_number: 7,
      context_route: {
        class: "intent",
        intent: "git.commit",
        confidence: 0.82,
        alternatives: [{ class: "help", confidence: 0.25 }],
        decision_id: "sess-1:7",
      },
    };
    store.applyTurnResult(result);
    const agent = store.transcript.find((e) => e.role === "agent");
    expect(agent).toBeDefined();
    expect(agent!.contextRoute).toEqual(result.context_route);
    // chatEntries passes the agent entry through unchanged, so the receipt
    // reaches the ChatTranscript component.
    const lastChat = store.chatEntries[store.chatEntries.length - 1];
    expect(lastChat!.contextRoute?.decision_id).toBe("sess-1:7");
  });

  it("carries parked_workspace onto the agent bubble so the fallback capsule badge renders", () => {
    const store = useRunStore();
    const result: TurnResult = {
      mode: "transitioned",
      state: "workbench",
      view: "Workbench ready.",
      turn_number: 8,
      parked_workspace: {
        source_workspace: "/tmp/workspaces/park-source",
        recovery_workspace: "/tmp/workspaces/park-source-park-20260711T083617-64827",
        recovery_branch: "agent/park-source-park-20260711T083617-64827",
        recovery_commit: "0123456789abcdef0123456789abcdef01234567",
        cleaned: true,
        reason: "operator reroute",
      },
    };
    store.applyTurnResult(result);
    const agent = store.transcript.find((e) => e.role === "agent");
    expect(agent).toBeDefined();
    expect(agent!.parkedWorkspace).toEqual(result.parked_workspace);
    const lastChat = store.chatEntries[store.chatEntries.length - 1];
    expect(lastChat!.parkedWorkspace?.recovery_branch).toContain("agent/park-source-park");
  });

  it("leaves contextRoute unset for a non-contextual turn", () => {
    const store = useRunStore();
    store.applyTurnResult({
      mode: "transitioned",
      state: "lobby",
      view: "A normal room view.",
      turn_number: 2,
    });
    const agent = store.transcript.find((e) => e.role === "agent");
    expect(agent).toBeDefined();
    expect(agent!.contextRoute).toBeUndefined();
  });
});
