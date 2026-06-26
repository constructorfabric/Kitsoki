/**
 * REGRESSION — bug 2026-06-12T022703Z-web-transport-hides-background-completion-failure
 *
 * "Web transport doesn't surface a background_completion turn's failure
 *  (last_error / say), so a failed background job looks hung."
 *
 * When a background job fails, the engine fires a scheduler-driven
 * `background_completion` turn that:
 *   - enters the destination state (machine.state_entered),
 *   - sets world.last_error,
 *   - emits a machine.say describing the failure,
 *   - ends the turn with outcome=background_completion.
 *
 * That turn is NOT driven by an inbound write RPC — it lands purely over the
 * SSE subscription. The web run store's subscription callback
 * (src/stores/run.ts hydrate()) used to only do
 *     events.value.push(e); applyStatePath(e)
 * — it refreshed `currentStatePath` but NEVER refreshed `currentView`.
 * `currentView` is written by the RPC-driven write paths (applyTurnResult /
 * loadInitialView / rehydrate / resetSessionState), none of which fire for a
 * scheduler-driven completion. So the operator kept seeing the stale
 * pre-completion "…executing" view; the failure say / last_error were dropped
 * and the session "looked hung".
 *
 * The fix adds maybeRefreshViewOnBackgroundCompletion: on the terminal
 * turn.end (outcome=background_completion) it re-pulls the room view and
 * mirrors it into currentView / currentStatePath / terminal, pushing an agent
 * transcript entry — mirroring how the TUI re-renders on completion.
 *
 * This test exercises the gap deterministically with NO live server and NO LLM:
 * it captures the SSE callback, seeds the pre-completion view, then plays the
 * exact background_completion event sequence from the filed trace
 * (94c6daa4-web-…jsonl, turn 5) and asserts currentView now advances to the
 * failed room view.
 */

import { describe, it, expect, beforeEach } from "vitest";
import { setActivePinia, createPinia } from "pinia";
import { useRunStore } from "../../src/stores/run.js";
import type { DataSource } from "../../src/data/source.js";
import type { TraceEvent, TurnResult } from "../../src/types.js";

const writeStubs = {
  submit: () => Promise.reject(new Error("not stubbed")) as Promise<TurnResult>,
  sendTurn: () => Promise.reject(new Error("not stubbed")) as Promise<TurnResult>,
  continueTurn: () => Promise.reject(new Error("not stubbed")) as Promise<TurnResult>,
  offpath: () => Promise.reject(new Error("not stubbed")) as Promise<{ answer: string }>,
};

// The pre-completion view the operator is looking at: the room is running its
// background job, so the rendered view says "executing…".
const EXECUTING_VIEW: TurnResult = {
  mode: "running",
  state: "context_extraction/executing",
  view: "Context Extraction — executing…",
  typed_view: { Source: "", Elements: [{ Kind: "paragraph", Source: "Context Extraction — executing…" }] },
  turn_number: 4,
};

// The view the engine renders once the background_completion turn has landed in
// the destination `…/failed` state: it carries the failure say / last_error the
// operator must see.
const FAILED_VIEW: TurnResult = {
  mode: "running",
  state: "context_extraction/failed",
  view: "Context Extraction job ended with status `failed`. Type `quit` to abort.",
  typed_view: {
    Source: "",
    Elements: [
      {
        Kind: "paragraph",
        Source: "Context Extraction job ended with status `failed`. Type `quit` to abort.",
      },
    ],
  },
  turn_number: 5,
};

/**
 * A DataSource whose subscribe captures the SSE onEvent callback and whose
 * `view()` returns whatever the current room renders. The test mutates
 * `viewResult` to model the engine advancing to the failed room.
 */
function liveSourceCapturing(
  onCap: (cb: (e: TraceEvent) => void) => void,
  getViewResult: () => TurnResult
): DataSource {
  return {
    listSessions: () => Promise.resolve([]),
    getSession: () =>
      Promise.resolve({
        session_id: "sess-1",
        app_id: "bugfix",
        current_state: "context_extraction/executing",
        turn: 4,
        started_at: "2026-06-12T02:27:00Z",
        terminal: false,
      }),
    getApp: () => Promise.resolve({ id: "bugfix", name: "Bugfix", root: "root", states: {} }),
    getMermaid: () => Promise.resolve({ source: "", node_map: {} }),
    getTrace: () => Promise.resolve({ events: [], last_turn: 4 }),
    // loadInitialView / the background-completion refresh read the current room
    // view; it reflects whichever room the session is sitting in.
    view: () => Promise.resolve(getViewResult()),
    subscribe: (_sid, onEvent) => {
      onCap(onEvent);
      return () => undefined;
    },
    ...writeStubs,
  } as DataSource;
}

function ev(msg: string, statePath: string, attrs: Record<string, unknown>): TraceEvent {
  return {
    time: "2026-06-12T02:27:03Z",
    level: "info",
    msg,
    session_id: "sess-1",
    turn: 5,
    state_path: statePath,
    attrs,
  };
}

// Let any pending microtasks (the async view-refresh chain) settle.
const flush = () => new Promise((r) => setTimeout(r, 0));

describe("run store — background_completion failure surfacing (web transport)", () => {
  beforeEach(() => setActivePinia(createPinia()));

  it("a scheduler-driven background_completion turn refreshes currentView, so last_error/say surface", async () => {
    let cb!: (e: TraceEvent) => void;
    // The room view advances to the failed state once the completion lands; the
    // engine's view RPC returns the failed room from then on.
    let viewResult: TurnResult = EXECUTING_VIEW;
    const store = useRunStore();
    const src = liveSourceCapturing(
      (c) => { cb = c; },
      () => viewResult
    );

    await store.hydrate(src, "sess-1");
    await store.loadInitialView(src, "sess-1"); // operator is watching the "executing…" view

    expect(store.currentView?.view).toBe("Context Extraction — executing…");
    expect(store.currentStatePath).toBe("context_extraction/executing");

    // ── The background job fails. The engine fires the background_completion
    //    turn (turn 5 of 94c6daa4-web-…jsonl) ENTIRELY over SSE — no write RPC.
    cb(ev("machine.state_entered", "context_extraction/failed", {}));
    cb(ev("world.update", "context_extraction/failed", {
      key: "last_error",
      value: "Context Extraction job ended with status: failed",
    }));
    cb(ev("machine.say", "context_extraction/failed", {
      text: "Context Extraction job ended with status `failed`. Type `quit` to abort.",
    }));
    cb(ev("scheduler.completed", "context_extraction/failed", {
      status: "failed",
      error: "host.agent.decide: claude exec failed: context canceled",
    }));
    // By the time the completion turn ends, the engine renders the failed room.
    viewResult = FAILED_VIEW;
    cb(ev("turn.end", "context_extraction/executing", { outcome: "background_completion" }));

    // The async view refresh settles on the next microtask tick.
    await flush();

    // The landed state advanced (applyStatePath handles state_entered)…
    expect(store.currentStatePath).toBe("context_extraction/failed");

    // …and currentView now reflects the failed room view: the failure `say` /
    // `world.last_error` reach the operator instead of the stale "executing…".
    expect(store.currentView?.state).toBe("context_extraction/failed");
    expect((store.currentView?.view ?? "").toLowerCase()).toContain("failed");

    // The failure narration also lands in the conversation transcript.
    const agentBubble = store.transcript.find(
      (t) => t.role === "agent" && t.text.toLowerCase().includes("failed")
    );
    expect(agentBubble).toBeDefined();

    // The diagnostics remain present in the trace the engine emitted.
    const failureSay = store.events.find(
      (e) => e.msg === "machine.say" && String(e.attrs?.text ?? "").includes("failed")
    );
    expect(failureSay).toBeDefined();
    const lastErrorUpdate = store.events.find(
      (e) => e.msg === "world.update" && e.attrs?.key === "last_error"
    );
    expect(lastErrorUpdate).toBeDefined();
  });
});
