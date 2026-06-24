/**
 * Component tests for src/components/ChapterTimeline.vue.
 *
 * Given a fixture chapter list: markers render at the right positions, a marker
 * click emits `seek`, and a drag across the track emits a `[start,end]` range.
 * No server, no LLM — pure component behaviour.
 */
import { describe, it, expect } from "vitest";
import { mount } from "@vue/test-utils";
import ChapterTimeline from "../../src/components/ChapterTimeline.vue";
import type { Chapter } from "../../src/data/source.js";

function chapter(over: Partial<Chapter> = {}): Chapter {
  return {
    index: 0,
    id: "c0",
    label: "Scene",
    start_ms: 0,
    end_ms: 1000,
    source_ref: { kind: "slidey", spec_path: "deck.json", scene_id: "s0" },
    ...over,
  };
}

const chapters: Chapter[] = [
  chapter({ index: 0, id: "intro", label: "Intro", start_ms: 0, end_ms: 4000 }),
  chapter({ index: 1, id: "run", label: "Run view", start_ms: 4000, end_ms: 8000 }),
  chapter({ index: 2, id: "end", label: "Outro", start_ms: 8000, end_ms: 10000 }),
];

describe("ChapterTimeline", () => {
  it("renders one marker per chapter at the right position", () => {
    const w = mount(ChapterTimeline, {
      props: { chapters, totalMs: 10000, selection: null },
    });
    const markers = w.findAll("[data-testid^='ct-marker-']");
    expect(markers).toHaveLength(3);

    // intro at 0ms → 0%, run at 4000/10000 → 40%, end at 8000 → 80%.
    expect(w.get("[data-testid='ct-marker-intro']").attributes("style")).toContain(
      "left: 0%"
    );
    expect(w.get("[data-testid='ct-marker-run']").attributes("style")).toContain(
      "left: 40%"
    );
    expect(w.get("[data-testid='ct-marker-end']").attributes("style")).toContain(
      "left: 80%"
    );
  });

  it("emits seek with the chapter start_ms when a marker is clicked", async () => {
    const w = mount(ChapterTimeline, {
      props: { chapters, totalMs: 10000, selection: null },
    });
    await w.get("[data-testid='ct-marker-run']").trigger("click");
    const ev = w.emitted("seek");
    expect(ev).toBeTruthy();
    expect(ev![0]).toEqual([4000]);
  });

  it("emits a [start,end] range when dragged across the track", async () => {
    const w = mount(ChapterTimeline, {
      props: { chapters, totalMs: 10000, selection: null },
    });
    const track = w.get("[data-testid='ct-track']");
    // Stub the track geometry: 0..1000px maps to 0..10000ms.
    (track.element as HTMLElement).getBoundingClientRect = () =>
      ({ left: 0, width: 1000, top: 0, height: 28, right: 1000, bottom: 28 }) as DOMRect;

    await track.trigger("pointerdown", { clientX: 200 }); // 2000ms
    await track.trigger("pointerup", { clientX: 700 }); // 7000ms

    const ev = w.emitted("select");
    expect(ev).toBeTruthy();
    expect(ev![0]).toEqual([{ start_ms: 2000, end_ms: 7000 }]);
  });

  it("orders a backwards drag into [min,max]", async () => {
    const w = mount(ChapterTimeline, {
      props: { chapters, totalMs: 10000, selection: null },
    });
    const track = w.get("[data-testid='ct-track']");
    (track.element as HTMLElement).getBoundingClientRect = () =>
      ({ left: 0, width: 1000, top: 0, height: 28, right: 1000, bottom: 28 }) as DOMRect;

    await track.trigger("pointerdown", { clientX: 800 }); // 8000ms
    await track.trigger("pointerup", { clientX: 300 }); // 3000ms
    expect(w.emitted("select")![0]).toEqual([{ start_ms: 3000, end_ms: 8000 }]);
  });

  it("emits flag with the current selection", async () => {
    const w = mount(ChapterTimeline, {
      props: {
        chapters,
        totalMs: 10000,
        selection: { start_ms: 4000, end_ms: 6000 },
      },
    });
    await w.get("[data-testid='ct-flag-btn']").trigger("click");
    expect(w.emitted("flag")![0]).toEqual([{ start_ms: 4000, end_ms: 6000 }]);
  });

  it("disables flag when there is no selection", () => {
    const w = mount(ChapterTimeline, {
      props: { chapters, totalMs: 10000, selection: null },
    });
    expect(
      w.get("[data-testid='ct-flag-btn']").attributes("disabled")
    ).toBeDefined();
  });

  it("spaces markers evenly when chapter windows are zero-width", () => {
    const zero = chapters.map((c) => ({ ...c, start_ms: 0, end_ms: 0 }));
    const w = mount(ChapterTimeline, {
      props: { chapters: zero, totalMs: 0, selection: null },
    });
    // 3 markers evenly: 0%, 50%, 100%.
    const styles = w
      .findAll("[data-testid^='ct-marker-']")
      .map((m) => m.attributes("style"));
    expect(styles[0]).toContain("left: 0%");
    expect(styles[1]).toContain("left: 50%");
    expect(styles[2]).toContain("left: 100%");
  });
});
