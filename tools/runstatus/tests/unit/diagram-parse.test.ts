import { describe, it, expect } from "vitest";
import { parseDiagram } from "../../src/diagram/parse.js";

const CLOAK_SRC = `%% Cloak of Darkness
%% kitsoki viz --flowchart --detail states
flowchart LR

  Start(["<b>Start</b>"]):::input

  subgraph SG_bar["<b>phase 1 · bar</b> — A small bar off the foyer."]
    direction LR
    ST_bar_dark[/"bar.dark"/]:::room
    ST_bar_lit[/"bar.lit"/]:::room
  end

  subgraph SG_cloakroom["<b>phase 2 · cloakroom</b> — A small cloakroom."]
    direction LR
    ST_cloakroom[/"cloakroom"/]:::room
  end

  subgraph SG_ended["<b>phase 3 · ended</b> — The journey is over."]
    direction LR
    ST_ended[/"ended"/]:::room
  end

  subgraph SG_foyer["<b>phase 0 · foyer</b> — The entrance hall."]
    direction LR
    ST_foyer[/"foyer"/]:::room
  end

  Start --> ST_foyer

  ST_bar_lit -- "read_message" --> ST_ended
  ST_bar_lit -- "go" --> ST_foyer
  ST_foyer -- "go" --> ST_bar
  ST_foyer -- "go" --> ST_cloakroom
  ST_foyer -- "look" --> ST_foyer
  ST_cloakroom -- "go" --> ST_foyer
`;

describe("parseDiagram — cloak", () => {
  it("parses all four phases, in topological order from Start", () => {
    const d = parseDiagram(CLOAK_SRC);
    expect(d.phases.map((p) => p.name)).toEqual(["foyer", "bar", "cloakroom", "ended"]);
  });

  it("strips <b>...</b> and splits desc on ' — '", () => {
    const d = parseDiagram(CLOAK_SRC);
    const foyer = d.phases.find((p) => p.name === "foyer")!;
    expect(foyer.phaseNumber).toBe(0);
    expect(foyer.desc).toBe("The entrance hall.");
  });

  it("captures rooms inside their owning phase", () => {
    const d = parseDiagram(CLOAK_SRC);
    const bar = d.phases.find((p) => p.name === "bar")!;
    expect(bar.rooms.map((r) => r.id).sort()).toEqual(["ST_bar_dark", "ST_bar_lit"]);
  });

  it("captures labelled edges and self-loop flag", () => {
    const d = parseDiagram(CLOAK_SRC);
    const selfLoops = d.edges.filter((e) => e.selfLoop);
    expect(selfLoops.length).toBeGreaterThan(0);
    expect(selfLoops.some((e) => e.from === "ST_foyer" && e.label === "look")).toBe(true);
  });

  it("captures the Start node and start room", () => {
    const d = parseDiagram(CLOAK_SRC);
    expect(d.startId).toBe("Start");
    expect(d.startRoomId).toBe("ST_foyer");
  });
});

const BUGFIX_SRC = `flowchart LR

  Start(["<b>Start</b>"]):::input

  subgraph SG_idle["<b>phase 0 · idle</b> — Parked. Waiting."]
    direction LR
    ST_idle[/"idle"/]:::room
  end

  subgraph SG_validating["<b>phase 2 · validating</b> — Validate the fix."]
    direction LR
    ST_validating[/"validating"/]:::room
  end

  subgraph SG_reproducing["<b>phase 1 · reproducing</b> — Reproduce the bug."]
    direction LR
    ST_reproducing[/"reproducing"/]:::room
  end

  subgraph SG_proposing["<b>phase 4 · proposing</b> — Draft the fix."]
    direction LR
    ST_proposing[/"proposing"/]:::room
  end

  subgraph SG_done["<b>phase 3 · done</b> — Close-out."]
    direction LR
    ST_done[/"done"/]:::room
  end

  Start --> ST_idle

  ST_idle -- "start" --> ST_reproducing
  ST_reproducing -- "accept" --> ST_proposing
  ST_proposing -- "accept" --> ST_validating
  ST_validating -- "accept" --> ST_done
`;

describe("parseDiagram — bugfix-shaped (declared phase numbers are misleading)", () => {
  it("orders phases by graph-distance from Start, not by declared phase number", () => {
    // Declared phase numbers: idle(0), reproducing(1), validating(2), done(3), proposing(4).
    // But the actual visit order is idle → reproducing → proposing → validating → done.
    const d = parseDiagram(BUGFIX_SRC);
    expect(d.phases.map((p) => p.name)).toEqual([
      "idle",
      "reproducing",
      "proposing",
      "validating",
      "done",
    ]);
  });
});
