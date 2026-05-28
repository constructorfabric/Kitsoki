/**
 * Unit tests for src/data/snapshot-source.ts
 */

import { describe, it, expect, vi } from "vitest";
import { SnapshotSource } from "../../src/data/snapshot-source.js";
import type { Snapshot } from "../../src/types.js";

const FIXTURE: Snapshot = {
  session: {
    session_id: "test-session",
    app_id: "test-app",
    current_state: "root/active",
    turn: 5,
    started_at: "2026-01-01T00:00:00Z",
    terminal: false,
  },
  app: {
    id: "test-app",
    name: "Test App",
    root: "root",
    states: {
      "root/active": { description: "Active state" },
      "root/done": { description: "Done state" },
    },
  },
  mermaid: {
    source: "flowchart LR\n  root_active --> root_done",
    node_map: {
      root_active: { kind: "state", ref: "root/active" },
      root_done: { kind: "state", ref: "root/done" },
    },
  },
  events: [
    {
      time: "2026-01-01T00:00:01Z",
      level: "info",
      msg: "TurnStarted",
      session_id: "test-session",
      turn: 1,
      state_path: "root/active",
      attrs: { foo: "bar" },
    },
    {
      time: "2026-01-01T00:00:02Z",
      level: "info",
      msg: "LLMCalled",
      session_id: "test-session",
      turn: 2,
      state_path: "root/active",
      attrs: { tokens: 42 },
    },
    {
      time: "2026-01-01T00:00:03Z",
      level: "info",
      msg: "TransitionApplied",
      session_id: "test-session",
      turn: 3,
      state_path: "root/done",
      attrs: {},
    },
  ],
};

describe("SnapshotSource", () => {
  it("listSessions returns the single session header", async () => {
    const src = new SnapshotSource(FIXTURE);
    const sessions = await src.listSessions();
    expect(sessions).toHaveLength(1);
    expect(sessions[0]!.session_id).toBe("test-session");
  });

  it("getSession returns the session header (ignores sessionId arg)", async () => {
    const src = new SnapshotSource(FIXTURE);
    const session = await src.getSession("anything");
    expect(session.app_id).toBe("test-app");
    expect(session.current_state).toBe("root/active");
    expect(session.turn).toBe(5);
  });

  it("getApp returns the app definition", async () => {
    const src = new SnapshotSource(FIXTURE);
    const app = await src.getApp("anything");
    expect(app.id).toBe("test-app");
    expect(app.name).toBe("Test App");
    expect(Object.keys(app.states)).toHaveLength(2);
  });

  it("getMermaid returns the mermaid snapshot", async () => {
    const src = new SnapshotSource(FIXTURE);
    const mer = await src.getMermaid("anything");
    expect(mer.source).toContain("flowchart LR");
    expect(mer.node_map["root_active"]).toEqual({
      kind: "state",
      ref: "root/active",
    });
  });

  it("getTrace returns all events by default", async () => {
    const src = new SnapshotSource(FIXTURE);
    const { events, last_turn } = await src.getTrace("anything");
    expect(events).toHaveLength(3);
    expect(last_turn).toBe(3);
  });

  it("getTrace filters by since_turn", async () => {
    const src = new SnapshotSource(FIXTURE);
    const { events } = await src.getTrace("anything", { since_turn: 2 });
    expect(events).toHaveLength(2);
    expect(events[0]!.turn).toBe(2);
  });

  it("getTrace filters by until_turn", async () => {
    const src = new SnapshotSource(FIXTURE);
    const { events } = await src.getTrace("anything", { until_turn: 2 });
    expect(events).toHaveLength(2);
    expect(events.at(-1)!.turn).toBe(2);
  });

  it("getTrace respects limit", async () => {
    const src = new SnapshotSource(FIXTURE);
    const { events } = await src.getTrace("anything", { limit: 1 });
    expect(events).toHaveLength(1);
    expect(events[0]!.turn).toBe(1);
  });

  it("subscribe returns a no-op unsubscribe and never calls onEvent", async () => {
    const src = new SnapshotSource(FIXTURE);
    const onEvent = vi.fn();
    const unsub = src.subscribe("anything", onEvent);

    // Give async tasks time — onEvent must never be called.
    await new Promise<void>((r) => setTimeout(r, 10));
    expect(onEvent).not.toHaveBeenCalled();

    // Calling unsub must not throw.
    expect(() => unsub()).not.toThrow();
  });

  it("throws if no snapshot is provided and window.__KITSOKI_SNAPSHOT__ is undefined", () => {
    expect(() => new SnapshotSource(undefined)).toThrow(
      /no snapshot provided/
    );
  });
});
