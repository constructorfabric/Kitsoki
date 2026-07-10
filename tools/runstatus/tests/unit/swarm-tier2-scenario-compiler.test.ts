/**
 * Unit tests for tools/swarm/tiers/tier2.ts's scenario-driven compiler (task
 * 3.2, docs/proposals/scenario-foundry.md): loadScenarioFixtures,
 * parsePersonaMix, selectScenarioForIndex, scenarioCannedMessage,
 * buildTier2RecordingFromScenarios, and buildTier2RecordingAuto's env-var
 * wiring / fallback. No browser, no server, no LLM — pure functions + a
 * scratch temp dir.
 */
import { describe, it, expect, afterEach } from "vitest";
import fs from "fs";
import os from "os";
import path from "path";
import {
  loadScenarioFixtures,
  parsePersonaMix,
  selectScenarioForIndex,
  scenarioCannedMessage,
  buildTier2RecordingFromScenarios,
  buildTier2RecordingAuto,
  buildTier2Recording,
  OFF_MENU_QUESTION,
  type ScenarioFixture,
} from "../../../swarm/tiers/tier2.js";

const scratchDirs: string[] = [];
function mkScratch(): string {
  const d = fs.mkdtempSync(path.join(os.tmpdir(), "kitsoki-tier2-test-"));
  scratchDirs.push(d);
  return d;
}
afterEach(() => {
  for (const d of scratchDirs.splice(0)) fs.rmSync(d, { recursive: true, force: true });
});

function writeScenario(dir: string, doc: Record<string, unknown>): string {
  const p = path.join(dir, `${doc.id}.json`);
  fs.writeFileSync(p, JSON.stringify(doc));
  return p;
}

function scenarioDoc(id: string, persona: string, over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    schema_version: "1.0",
    kind: "conversation",
    id,
    source: "mined",
    provenance: { corpus: "claude-code", session_id: "s", span_idx: 0 },
    persona,
    goal: "do the thing",
    turns: [{ role: "user", text: `utterance for ${id}`, corrected: false }],
    expected_effects: [],
    abandoned: false,
    ...over,
  };
}

describe("loadScenarioFixtures", () => {
  it("loads every scn-*.json in a directory, skipping non-matching files", () => {
    const dir = mkScratch();
    writeScenario(dir, scenarioDoc("scn-a-0000", "explorer"));
    writeScenario(dir, scenarioDoc("scn-b-0000", "core-maintainer"));
    fs.writeFileSync(path.join(dir, "MANIFEST.md"), "not json");
    fs.writeFileSync(path.join(dir, "scn-c-0000.json"), "{ not valid json");
    const scenarios = loadScenarioFixtures(dir);
    expect(scenarios.map((s) => s.id).sort()).toEqual(["scn-a-0000", "scn-b-0000"]);
  });

  it("loads a single scenario file directly", () => {
    const dir = mkScratch();
    const p = writeScenario(dir, scenarioDoc("scn-solo-0000", "explorer"));
    const scenarios = loadScenarioFixtures(p);
    expect(scenarios).toHaveLength(1);
    expect(scenarios[0].id).toBe("scn-solo-0000");
  });

  it("skips a document that isn't kind: conversation", () => {
    const dir = mkScratch();
    writeScenario(dir, { ...scenarioDoc("scn-bad-0000", "explorer"), kind: "trace" });
    expect(loadScenarioFixtures(dir)).toHaveLength(0);
  });
});

describe("parsePersonaMix", () => {
  it("splits on commas and trims", () => {
    expect(parsePersonaMix("explorer, core-maintainer ,bugfix-contributor")).toEqual([
      "explorer",
      "core-maintainer",
      "bugfix-contributor",
    ]);
  });
  it("drops a weight suffix (core-maintainer:heavy -> core-maintainer)", () => {
    expect(parsePersonaMix("core-maintainer:heavy")).toEqual(["core-maintainer"]);
  });
  it("returns [] for empty/whitespace", () => {
    expect(parsePersonaMix("")).toEqual([]);
    expect(parsePersonaMix("   ")).toEqual([]);
  });
});

describe("selectScenarioForIndex", () => {
  const scenarios: ScenarioFixture[] = [
    scenarioDoc("scn-a-0000", "explorer") as unknown as ScenarioFixture,
    scenarioDoc("scn-b-0000", "core-maintainer") as unknown as ScenarioFixture,
  ];

  it("with no persona mix, cycles the full list by index", () => {
    expect(selectScenarioForIndex(scenarios, [], 0).id).toBe("scn-a-0000");
    expect(selectScenarioForIndex(scenarios, [], 1).id).toBe("scn-b-0000");
    expect(selectScenarioForIndex(scenarios, [], 2).id).toBe("scn-a-0000"); // wraps
  });

  it("prefers an exact persona match for the mix entry at this index", () => {
    expect(selectScenarioForIndex(scenarios, ["core-maintainer"], 0).id).toBe("scn-b-0000");
  });

  it("falls back to the full list when the mix matches nothing", () => {
    expect(selectScenarioForIndex(scenarios, ["nonexistent-persona"], 0).id).toBe("scn-a-0000");
  });

  it("throws on an empty scenario list", () => {
    expect(() => selectScenarioForIndex([], [], 0)).toThrow();
  });
});

describe("scenarioCannedMessage", () => {
  it("cites corrective_ops when the (only) turn was corrected", () => {
    const s = scenarioDoc("scn-x-0000", "explorer", {
      turns: [{ role: "user", text: "t", corrected: true, corrective_ops: ["git rebase --abort"] }],
    }) as unknown as ScenarioFixture;
    expect(scenarioCannedMessage(s)).toContain("git rebase --abort");
  });

  it("cites expected_effects for a single-turn scenario with grounded effects", () => {
    const s = scenarioDoc("scn-y-0000", "explorer", {
      expected_effects: ["git.rebase completed"],
    }) as unknown as ScenarioFixture;
    expect(scenarioCannedMessage(s)).toContain("git.rebase completed");
  });

  it("falls back to the generic canned message otherwise", () => {
    const s = scenarioDoc("scn-z-0000", "explorer") as unknown as ScenarioFixture;
    expect(scenarioCannedMessage(s)).toBe("I don't have a menu item for that — let me just answer it.");
  });
});

describe("buildTier2RecordingFromScenarios", () => {
  it("writes one clarify entry per marker, sourced from the selected scenario's first turn", () => {
    const scenarios = [
      scenarioDoc("scn-a-0000", "explorer") as unknown as ScenarioFixture,
      scenarioDoc("scn-b-0000", "core-maintainer") as unknown as ScenarioFixture,
    ];
    const dir = mkScratch();
    const outPath = buildTier2RecordingFromScenarios(scenarios, [], ["m0", "m1"], dir);
    const text = fs.readFileSync(outPath, "utf8");
    expect(text).toContain("m0 :: utterance for scn-a-0000");
    expect(text).toContain("m1 :: utterance for scn-b-0000");
    expect((text.match(/clarify: true/g) ?? []).length).toBe(2);
  });
});

describe("buildTier2RecordingAuto", () => {
  it("falls back to the hardcoded off-menu question when SWARM_FIXTURE is unset", () => {
    const dir = mkScratch();
    const outPath = buildTier2RecordingAuto(["m0"], dir, {});
    const text = fs.readFileSync(outPath, "utf8");
    expect(text).toContain(OFF_MENU_QUESTION);
  });

  it("falls back when SWARM_FIXTURE points at a dir with no usable scenarios", () => {
    const emptyDir = mkScratch();
    const outDir = mkScratch();
    const outPath = buildTier2RecordingAuto(["m0"], outDir, { SWARM_FIXTURE: emptyDir });
    const text = fs.readFileSync(outPath, "utf8");
    expect(text).toContain(OFF_MENU_QUESTION);
  });

  it("is byte-identical to buildTier2Recording's direct output when env vars are unset", () => {
    const dir1 = mkScratch();
    const dir2 = mkScratch();
    const auto = fs.readFileSync(buildTier2RecordingAuto(["m0", "m1"], dir1, {}), "utf8");
    const direct = fs.readFileSync(buildTier2Recording(["m0", "m1"], dir2), "utf8");
    // generated_at timestamps differ by construction; strip them before compare.
    const strip = (s: string) => s.replace(/generated_at: ".*"/, "generated_at: STRIPPED");
    expect(strip(auto)).toBe(strip(direct));
  });

  it("consumes SWARM_FIXTURE + SWARM_PERSONA_MIX when both are set", () => {
    const fixtureDir = mkScratch();
    writeScenario(fixtureDir, scenarioDoc("scn-a-0000", "explorer"));
    writeScenario(fixtureDir, scenarioDoc("scn-b-0000", "core-maintainer"));
    const outDir = mkScratch();
    const outPath = buildTier2RecordingAuto(["m0"], outDir, {
      SWARM_FIXTURE: fixtureDir,
      SWARM_PERSONA_MIX: "core-maintainer",
    });
    const text = fs.readFileSync(outPath, "utf8");
    expect(text).toContain("m0 :: utterance for scn-b-0000");
    expect(text).not.toContain(OFF_MENU_QUESTION);
  });
});
