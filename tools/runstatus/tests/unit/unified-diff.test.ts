/**
 * Unit tests for the UnifiedDiff parsing logic used in UnifiedDiff.vue.
 * We test the pure parsing function directly (duplicated here to keep it
 * framework-independent) rather than mounting the component.
 */

import { describe, it, expect } from "vitest";

// ----- duplicated parser from UnifiedDiff.vue (kept in sync) ----------------

interface HunkBlock {
  header: string;
  lines: string[];
}

function parseDiff(diff: string): HunkBlock[] {
  const blocks: HunkBlock[] = [];
  let current: HunkBlock | null = null;

  for (const raw of diff.split("\n")) {
    if (/^(---|\+\+\+) /.test(raw)) continue;

    if (raw.startsWith("@@")) {
      if (current) blocks.push(current);
      current = { header: raw, lines: [] };
    } else if (current) {
      current.lines.push(raw);
    }
  }
  if (current) blocks.push(current);
  return blocks;
}

function lineClass(line: string): string {
  if (line.startsWith("+")) return "diff-add";
  if (line.startsWith("-")) return "diff-del";
  return "diff-ctx";
}

// ---------------------------------------------------------------------------

const SAMPLE_DIFF = `--- a/src/foo.ts
+++ b/src/foo.ts
@@ -1,4 +1,5 @@
 import { bar } from "./bar.js";
-const x = 1;
+const x = 2;
+const y = 3;
 export { x };
`;

describe("parseDiff", () => {
  it("skips --- and +++ file headers", () => {
    const blocks = parseDiff(SAMPLE_DIFF);
    for (const block of blocks) {
      for (const line of block.lines) {
        expect(line).not.toMatch(/^(---|\+\+\+) /);
      }
    }
  });

  it("splits into one hunk block", () => {
    const blocks = parseDiff(SAMPLE_DIFF);
    expect(blocks.length).toBe(1);
  });

  it("preserves the @@ hunk header", () => {
    const blocks = parseDiff(SAMPLE_DIFF);
    expect(blocks[0]?.header).toBe("@@ -1,4 +1,5 @@");
  });

  it("captures four content lines", () => {
    const blocks = parseDiff(SAMPLE_DIFF);
    // " import...", "-const x = 1;", "+const x = 2;", "+const y = 3;", " export..."
    // trailing empty line is also captured
    const lines = blocks[0]?.lines ?? [];
    expect(lines.some((l) => l === "-const x = 1;")).toBe(true);
    expect(lines.some((l) => l === "+const x = 2;")).toBe(true);
    expect(lines.some((l) => l === "+const y = 3;")).toBe(true);
  });

  it("handles multiple hunks", () => {
    const multi = `--- a/f\n+++ b/f\n@@ -1,1 +1,1 @@\n-old\n+new\n@@ -10,1 +10,1 @@\n-old2\n+new2\n`;
    const blocks = parseDiff(multi);
    expect(blocks.length).toBe(2);
    expect(blocks[0]?.header).toBe("@@ -1,1 +1,1 @@");
    expect(blocks[1]?.header).toBe("@@ -10,1 +10,1 @@");
  });

  it("returns empty array for empty string", () => {
    expect(parseDiff("")).toEqual([]);
  });

  it("returns empty array for diff with only file headers and no hunks", () => {
    expect(parseDiff("--- a/f\n+++ b/f\n")).toEqual([]);
  });
});

describe("lineClass", () => {
  it("classifies + lines as diff-add", () => {
    expect(lineClass("+added line")).toBe("diff-add");
  });
  it("classifies - lines as diff-del", () => {
    expect(lineClass("-removed line")).toBe("diff-del");
  });
  it("classifies space lines as diff-ctx", () => {
    expect(lineClass(" context line")).toBe("diff-ctx");
  });
  it("classifies empty lines as diff-ctx", () => {
    expect(lineClass("")).toBe("diff-ctx");
  });
});
