/**
 * Unit tests for the embed boot-param contract (src/lib/embedBoot.ts),
 * including the world_seed param (U4, feedback report
 * 01KXD23CJBVXQGQ08EXET3QC1X): context delivered IN the boot request so
 * autostart's session.new never races the host's first `context`
 * postMessage. No DOM, no server, no LLM — resolveEmbedBoot takes the
 * query string directly.
 */
import { describe, it, expect } from "vitest";
import { resolveEmbedBoot } from "../../src/lib/embedBoot.js";

describe("resolveEmbedBoot", () => {
  it("parses the §3.1 boot params", () => {
    const boot = resolveEmbedBoot(
      "?surface=chat&embed=1&story=stories/pog-graph-driver&autostart=1&catalog=pog&scope=feature-x&origin=http://localhost:5173",
    );
    expect(boot.story).toBe("stories/pog-graph-driver");
    expect(boot.autostart).toBe(true);
    expect(boot.catalog).toBe("pog");
    expect(boot.scope).toBe("feature-x");
    expect(boot.origin).toBe("http://localhost:5173");
    expect(boot.worldSeed).toBeNull();
  });

  it("defaults everything off for a bare URL", () => {
    const boot = resolveEmbedBoot("");
    expect(boot.story).toBeNull();
    expect(boot.autostart).toBe(false);
    expect(boot.worldSeed).toBeNull();
  });

  it("parses a URI-encoded world_seed JSON object", () => {
    const seed = {
      catalog: "pog",
      view: "dashboard",
      node_ids: ["feat-portal", "req-x"],
      instruction: "review the selected nodes",
    };
    const boot = resolveEmbedBoot(
      `?autostart=1&world_seed=${encodeURIComponent(JSON.stringify(seed))}`,
    );
    expect(boot.worldSeed).toEqual(seed);
  });

  it("degrades a malformed or non-object world_seed to null, never throws", () => {
    for (const bad of ["{not json", "42", '"str"', "[1,2]", "null"]) {
      const boot = resolveEmbedBoot(`?world_seed=${encodeURIComponent(bad)}`);
      expect(boot.worldSeed).toBeNull();
    }
  });
});
