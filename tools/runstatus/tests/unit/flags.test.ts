/**
 * Unit tests for the single flag→chapter resolver (src/lib/flags.ts).
 *
 * The label_consistency bug: the Flags-list label and the FlagDetail
 * source_ref must resolve to the SAME chapter for the same flag. Both derive
 * from dominantChapter, so this test pins that they agree at a mid-timeline
 * point — and that the resolver no longer collapses every flag to scene-0 when
 * given REAL contiguous windows (the producer fix). No DOM, no server, no LLM.
 */
import { describe, it, expect } from "vitest";
import { dominantChapter, sourceRefFor } from "../../src/lib/flags.js";
import type { Flag } from "../../src/lib/flags.js";
import type { Chapter } from "../../src/data/source.js";

// The demo deck's fixed sidecar: 6 scenes, even ~9984ms windows tiling [0,
// 59907] (the final window snaps to the total). This mirrors the regenerated
// .artifacts/review-video chapters.json exactly.
const evenMs = Math.floor(59907 / 6); // 9984
function demoChapters(): Chapter[] {
  const labels = [
    "Kitsoki",
    "narrative",
    "Story anatomy",
    "The directed cyclic graph",
    "narrative",
    "Room lifecycle",
  ];
  const chs: Chapter[] = [];
  let cursor = 0;
  for (let i = 0; i < 6; i++) {
    const start = cursor;
    const end = i === 5 ? 59907 : start + evenMs;
    cursor = end;
    chs.push({
      index: i,
      id: `scene-${i}`,
      label: labels[i],
      start_ms: start,
      end_ms: end,
      source_ref: { kind: "slidey", spec_path: "docs/decks/arch-and-usage.json", scene_id: `scene-${i}` },
    });
  }
  return chs;
}

describe("dominantChapter (single resolver)", () => {
  it("resolves a 0:26 point flag to scene-2, not scene-0", () => {
    const chs = demoChapters();
    const c = dominantChapter(chs, 26000, 26000);
    expect(c?.id).toBe("scene-2");
  });

  it("label and source_ref agree for the same flag at 0:26", () => {
    const chs = demoChapters();
    const chapter = dominantChapter(chs, 26000, 26000);
    const flag: Flag = {
      id: 1,
      start_ms: 26000,
      end_ms: 26000,
      chapter,
      frame_handle: "h",
      instruction: "",
      sent: false,
    };
    // Flags-list label path (FlagList.vue: f.chapter?.label).
    const listLabel = flag.chapter?.label;
    // FlagDetail source_ref path (sourceRefFor → flag.chapter.source_ref).
    const detailScene = sourceRefFor(flag)?.scene_id;
    // The detail "scene N" header is flag.chapter.index+1.
    expect(listLabel).toBe("Story anatomy");
    expect(detailScene).toBe("scene-2");
    // Same underlying chapter id — they cannot diverge.
    expect(flag.chapter?.id).toBe(detailScene);
  });

  it("never collapses every timestamp to scene-0 with real windows", () => {
    const chs = demoChapters();
    const ids = [5000, 15000, 26000, 35000, 45000, 55000].map(
      (t) => dominantChapter(chs, t, t)?.id
    );
    expect(ids).toEqual([
      "scene-0",
      "scene-1",
      "scene-2",
      "scene-3",
      "scene-4",
      "scene-5",
    ]);
  });

  it("a range flag picks the max-overlap chapter", () => {
    const chs = demoChapters();
    // [8000, 12000] overlaps scene-0 (8000..9984=1984) and scene-1
    // (9984..12000=2016) — scene-1 wins.
    expect(dominantChapter(chs, 8000, 12000)?.id).toBe("scene-1");
  });
});
