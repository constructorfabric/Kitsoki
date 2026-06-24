import { describe, it, expect } from "vitest";
import { parseDiagram } from "../../src/diagram/parse.js";
import {
  matchRoomId,
  traveledPath,
  horizon,
  spineAhead,
  roomSpineAhead,
  enteringIntents,
  type StateEnteredish,
  type HorizonIntent,
  type Transitionish,
} from "../../src/diagram/horizon.js";

// A small oregon-shaped mermaid: a hub (general_store) with a compound
// executing room per leg (initial child .traveling) and an awaiting_reply
// room, then a terminal. Mirrors the diagram-parse.test.ts style.
const TRAIL_SRC = `flowchart LR

  Start(["<b>Start</b>"]):::input

  subgraph SG_general_store["<b>phase 0 · general_store</b> — Outfit and leave."]
    direction LR
    ST_general_store[/"general_store.idle"/]:::room
  end

  subgraph SG_leg_a_executing["<b>phase 1 · leg_a_executing</b> — First leg."]
    direction LR
    ST_leg_a_traveling[/"leg_a_executing.traveling"/]:::room
  end

  subgraph SG_leg_a_awaiting["<b>phase 2 · leg_a_awaiting_reply</b> — Reply."]
    direction LR
    ST_leg_a_awaiting[/"leg_a_awaiting_reply"/]:::room
  end

  subgraph SG_leg_b_executing["<b>phase 3 · leg_b_executing</b> — Second leg."]
    direction LR
    ST_leg_b_traveling[/"leg_b_executing.traveling"/]:::room
  end

  subgraph SG_ended_won["<b>phase 4 · ended_won</b> — You made it."]
    direction LR
    ST_ended_won[/"ended_won"/]:::room
  end

  Start --> ST_general_store

  ST_general_store -- "leave_store" --> ST_leg_a_traveling
  ST_general_store -- "look" --> ST_general_store
  ST_leg_a_traveling -- "continue" --> ST_leg_a_awaiting
  ST_leg_a_traveling -- "set_pace" --> ST_leg_a_traveling
  ST_leg_a_traveling -- "quit" --> ST_general_store
  ST_leg_a_awaiting -- "continue" --> ST_leg_b_traveling
  ST_leg_b_traveling -- "continue" --> ST_ended_won
`;

describe("horizon — matchRoomId (longest-prefix)", () => {
  const d = parseDiagram(TRAIL_SRC);

  it("matches a compound landed path to its room by the longest prefix", () => {
    expect(matchRoomId("leg_a_executing.traveling", d)).toBe("ST_leg_a_traveling");
    expect(matchRoomId("general_store.idle", d)).toBe("ST_general_store");
    expect(matchRoomId("leg_a_awaiting_reply", d)).toBe("ST_leg_a_awaiting");
  });

  it("returns null for an unmatched path or empty input", () => {
    expect(matchRoomId("no_such_state", d)).toBeNull();
    expect(matchRoomId("", d)).toBeNull();
    expect(matchRoomId("x", null)).toBeNull();
  });
});

describe("horizon — traveledPath (TRACE tier)", () => {
  const d = parseDiagram(TRAIL_SRC);

  const events: StateEnteredish[] = [
    { msg: "machine.state_entered", state_path: "general_store.idle" },
    { msg: "turn.end", state_path: "general_store.idle" }, // ignored (not state_entered)
    { msg: "machine.state_entered", state_path: "leg_a_executing" }, // compound parent
    { msg: "machine.state_entered", state_path: "leg_a_executing.traveling" }, // child → same room as parent? no: distinct room
    { msg: "machine.state_entered", state_path: "leg_a_awaiting_reply" },
    { msg: "machine.state_entered", state_path: "leg_a_awaiting_reply" }, // consecutive dup
    { msg: "machine.state_entered", state_path: "leg_b_executing.traveling" },
  ];

  it("maps ordered state_entered events to rooms, dropping nulls + consecutive dups", () => {
    // "leg_a_executing" (parent, no declared room of that exact label) → null,
    // "leg_a_executing.traveling" → ST_leg_a_traveling. The duplicate awaiting
    // entry collapses.
    expect(traveledPath(events, d)).toEqual([
      "ST_general_store",
      "ST_leg_a_traveling",
      "ST_leg_a_awaiting",
      "ST_leg_b_traveling",
    ]);
  });

  it("returns [] with no diagram", () => {
    expect(traveledPath(events, null)).toEqual([]);
  });
});

describe("horizon — horizon (LIVE tier): intent → target + kind", () => {
  const d = parseDiagram(TRAIL_SRC);

  it("joins allowed intents to outgoing edges and classifies each", () => {
    const intents: HorizonIntent[] = [
      { name: "continue", title: "Keep going" },
      { name: "set_pace" }, // self-loop
      { name: "quit" }, // exit by name AND target = hub
      { name: "look" }, // no edge from leg_a_traveling → null target
    ];
    const arcs = horizon(intents, "ST_leg_a_traveling", d);
    expect(arcs).toEqual([
      { intent: "continue", label: "Keep going", targetRoomId: "ST_leg_a_awaiting", kind: "forward" },
      { intent: "set_pace", label: "set_pace", targetRoomId: "ST_leg_a_traveling", kind: "self" },
      { intent: "quit", label: "quit", targetRoomId: "ST_general_store", kind: "exit" },
      { intent: "look", label: "look", targetRoomId: null, kind: "forward" },
    ]);
  });

  it("classifies a hub self-loop intent (look) as self when on the hub room", () => {
    const arcs = horizon([{ name: "look" }, { name: "leave_store" }], "ST_general_store", d);
    expect(arcs[0]).toEqual({ intent: "look", label: "look", targetRoomId: "ST_general_store", kind: "self" });
    expect(arcs[1]).toEqual({
      intent: "leave_store",
      label: "leave_store",
      targetRoomId: "ST_leg_a_traveling",
      kind: "forward",
    });
  });

  it("returns [] with no current room", () => {
    expect(horizon([{ name: "x" }], null, d)).toEqual([]);
  });
});

describe("horizon — spineAhead (PROJECTION tier)", () => {
  const d = parseDiagram(TRAIL_SRC);

  it("returns the forward phases after the current one (excluding exits)", () => {
    const res = spineAhead("ST_general_store", d);
    expect(res.kind).toBe("spine");
    if (res.kind === "spine") {
      // ended_won is a terminal but not name-prefixed __exit__/ended exactly →
      // it's "ended_won", so it stays. After general_store: legs + ended_won.
      expect(res.phases.map((p) => p.name)).toEqual([
        "leg_a_executing",
        "leg_a_awaiting_reply",
        "leg_b_executing",
        "ended_won",
      ]);
    }
  });

  it("returns an empty spine at journey's end (last phase)", () => {
    const res = spineAhead("ST_ended_won", d);
    expect(res).toEqual({ kind: "spine", phases: [] });
  });

  it("falls back to branches when ≥2 phases share the minimum forward rank", () => {
    // A fan: hub branches to two phases both at distance 1 from it.
    const BRANCHY = `flowchart LR
  Start(["<b>Start</b>"]):::input
  subgraph SG_hub["<b>phase 0 · hub</b> — Fork."]
    direction LR
    ST_hub[/"hub"/]:::room
  end
  subgraph SG_left["<b>phase 1 · left</b> — Left."]
    direction LR
    ST_left[/"left"/]:::room
  end
  subgraph SG_right["<b>phase 1 · right</b> — Right."]
    direction LR
    ST_right[/"right"/]:::room
  end
  Start --> ST_hub
  ST_hub -- "go_left" --> ST_left
  ST_hub -- "go_right" --> ST_right
`;
    const bd = parseDiagram(BRANCHY);
    const res = spineAhead("ST_hub", bd);
    expect(res).toEqual({ kind: "branches", count: 2 });
  });

  it("returns empty spine with no current room", () => {
    expect(spineAhead(null, d)).toEqual({ kind: "spine", phases: [] });
  });
});

// A dev-story-shaped design pipeline: ALL design rooms in ONE phase, with a
// `change_existing` SHORTCUT (search → refine) that corrupts BFS distance
// (refine and materialize both land at distance 3). Carries `%% banner`
// side-channel comments like the real web viz path emits.
const DESIGN_SRC = `flowchart LR
  Start(["<b>Start</b>"]):::input
  subgraph SG_main["<b>phase 0 · main</b> — Hub."]
    direction LR
    ST_main[/"main"/]:::room
  end
  subgraph SG_design["<b>phase 1 · design</b> — Design pipeline."]
    direction LR
    ST_design[/"design"/]:::room
    ST_design_search[/"design_search"/]:::room
    ST_design_materialize[/"design_materialize"/]:::room
    ST_design_refine[/"design_refine"/]:::room
    ST_design_draft[/"design_draft"/]:::room
    ST_design_done[/"design_done"/]:::room
  end
  Start --> ST_main
  ST_main -- "go_idea" --> ST_design
  ST_design -- "discuss" --> ST_design_search
  ST_design -- "quit" --> ST_main
  ST_design_search -- "confirm" --> ST_design_materialize
  ST_design_search -- "change_existing" --> ST_design_refine
  ST_design_search -- "quit" --> ST_main
  ST_design_materialize -- "confirm" --> ST_design_refine
  ST_design_materialize -- "quit" --> ST_main
  ST_design_refine -- "advance_brief" --> ST_design_draft
  ST_design_refine -- "clarify" --> ST_design
  ST_design_refine -- "quit" --> ST_main
  ST_design_draft -- "accept" --> ST_design_done
  ST_design_draft -- "quit" --> ST_main
  ST_design_done -- "go_main" --> ST_main
%% banner design INTAKE
%% banner design_search SEARCHING
%% banner design_materialize PREPARING
%% banner design_refine BRIEF
%% banner design_draft DRAFTING
%% banner design_done PUBLISHED
`;

describe("parse — banner side-channel (declared projection metadata)", () => {
  const d = parseDiagram(DESIGN_SRC);

  it("attaches each room's `%% banner` text by label", () => {
    expect(d.roomById.get("ST_design")?.banner).toBe("INTAKE");
    expect(d.roomById.get("ST_design_search")?.banner).toBe("SEARCHING");
    expect(d.roomById.get("ST_design_materialize")?.banner).toBe("PREPARING");
    expect(d.roomById.get("ST_design_refine")?.banner).toBe("BRIEF");
    expect(d.roomById.get("ST_design_draft")?.banner).toBe("DRAFTING");
    expect(d.roomById.get("ST_design_done")?.banner).toBe("PUBLISHED");
  });

  it("leaves rooms without a banner comment undefined", () => {
    expect(d.roomById.get("ST_main")?.banner).toBeUndefined();
  });
});

describe("horizon — roomSpineAhead (ROOM-level PROJECTION)", () => {
  const d = parseDiagram(DESIGN_SRC);

  it("recovers the canonical pipeline despite the distance-corrupting shortcut", () => {
    // From design_search the greedy deepest-unvisited walk must yield the FULL
    // route materialize→refine→draft→done, NOT the change_existing shortcut that
    // skips materialize (which BFS distance alone would not disambiguate).
    const res = roomSpineAhead("ST_design_search", d, [
      "ST_main",
      "ST_design",
      "ST_design_search",
    ]);
    expect(res.rooms.map((r) => r.label)).toEqual([
      "design_materialize",
      "design_refine",
      "design_draft",
      "design_done",
    ]);
    expect(res.branched).toBe(true); // change_existing is a real alternative
  });

  it("projects the whole pipeline from the intake room", () => {
    const res = roomSpineAhead("ST_design", d, ["ST_main", "ST_design"]);
    expect(res.rooms.map((r) => r.label)).toEqual([
      "design_search",
      "design_materialize",
      "design_refine",
      "design_draft",
      "design_done",
    ]);
  });

  it("excludes the hub and escape intents, and stops at journey's end", () => {
    const res = roomSpineAhead("ST_design_done", d, []);
    expect(res.rooms).toEqual([]); // only go_main (to hub) remains → nothing ahead
  });

  it("returns empty with no current room or no diagram", () => {
    expect(roomSpineAhead(null, d)).toEqual({ rooms: [], branched: false });
    expect(roomSpineAhead("ST_design", null)).toEqual({ rooms: [], branched: false });
  });
});

describe("horizon — enteringIntents (TRACE provenance)", () => {
  const d = parseDiagram(DESIGN_SRC);

  it("maps each entered room to the intent that drove it from machine.transition", () => {
    const events: Transitionish[] = [
      { msg: "machine.transition", attrs: { intent: "go_idea", to: "design" } },
      { msg: "machine.state_entered" as unknown as string, attrs: { to: "design" } }, // ignored
      { msg: "machine.transition", attrs: { intent: "discuss", to: "design_search" } },
      { msg: "machine.transition", attrs: { intent: "confirm", to: "design_materialize" } },
    ];
    const m = enteringIntents(events, d);
    expect(m.get("ST_design")).toBe("go_idea");
    expect(m.get("ST_design_search")).toBe("discuss");
    expect(m.get("ST_design_materialize")).toBe("confirm");
  });

  it("keeps the most recent entry intent when a room is re-entered", () => {
    const events: Transitionish[] = [
      { msg: "machine.transition", attrs: { intent: "discuss", to: "design_search" } },
      { msg: "machine.transition", attrs: { intent: "regenerate", to: "design_search" } },
    ];
    expect(enteringIntents(events, d).get("ST_design_search")).toBe("regenerate");
  });
});
