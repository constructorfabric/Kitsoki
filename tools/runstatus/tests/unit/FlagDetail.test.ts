/**
 * Component tests for src/components/FlagDetail.vue.
 *
 * A flag with a captured still handle + a resolved source_ref renders the media
 * still, the VS Code deep-link, and the dispatch buttons; editing the
 * instruction and clicking "Send to refine" emits the correct feedback-note
 * payload. No server, no LLM.
 */
import { describe, it, expect, vi } from "vitest";
import { mount } from "@vue/test-utils";
import FlagDetail from "../../src/components/FlagDetail.vue";
import type { Flag } from "../../src/lib/flags.js";

// ViewElement resolves artifact URLs through createDataSource(); stub it so the
// media branch renders without a live/snapshot source.
vi.mock("../../src/data/source.js", async (orig) => {
  const actual = await orig<typeof import("../../src/data/source.js")>();
  return {
    ...actual,
    createDataSource: () => ({ artifactUrl: (h: string) => `/artifact/${h}` }),
  };
});

function flag(over: Partial<Flag> = {}): Flag {
  return {
    id: 2,
    start_ms: 14000,
    end_ms: 14000,
    chapter: {
      index: 2,
      id: "run",
      label: "Run view",
      start_ms: 12000,
      end_ms: 16000,
      source_ref: {
        kind: "slidey",
        spec_path: "deck.json",
        scene_id: "run_view",
        line: 42,
      },
    },
    frame_handle: "frame#deadbeef",
    instruction: "",
    sent: false,
    ...over,
  };
}

function mountDetail(f: Flag) {
  return mount(FlagDetail, {
    props: { flag: f, video: "demo_video#ab12cd34", chat: [], chatBusy: false },
  });
}

describe("FlagDetail", () => {
  it("renders the captured still as a media image", () => {
    const w = mountDetail(flag());
    const img = w.get("[data-testid='fd-still'] img");
    expect(img.attributes("src")).toBe("/artifact/frame#deadbeef");
  });

  it("renders the source_ref and a VS Code deep-link", () => {
    const w = mountDetail(flag());
    const ref = w.get("[data-testid='fd-source']");
    expect(ref.text()).toContain("deck.json");
    expect(ref.text()).toContain("run_view");
    const open = w.get("[data-testid='fd-open']");
    expect(open.attributes("href")).toBe("vscode://file/deck.json:42");
  });

  it("disables Send to refine until an instruction is typed", async () => {
    const w = mountDetail(flag());
    expect(
      w.get("[data-testid='fd-send-refine']").attributes("disabled")
    ).toBeDefined();

    await w.get("[data-testid='fd-instruction']").setValue("heading clips");
    expect(
      w.get("[data-testid='fd-send-refine']").attributes("disabled")
    ).toBeUndefined();
  });

  it("emits send-refine with the correct feedback-note payload", async () => {
    const w = mountDetail(flag());
    await w.get("[data-testid='fd-instruction']").setValue("heading clips on mobile");
    await w.get("[data-testid='fd-send-refine']").trigger("click");

    const ev = w.emitted("send-refine");
    expect(ev).toBeTruthy();
    expect(ev![0][0]).toEqual({
      video: "demo_video#ab12cd34",
      source_ref: {
        kind: "slidey",
        spec_path: "deck.json",
        scene_id: "run_view",
        line: 42,
      },
      time_range: { start_ms: 14000 }, // point flag → no end_ms
      frame_handle: "frame#deadbeef",
      instruction: "heading clips on mobile",
    });
  });

  it("carries end_ms for a range flag", async () => {
    const w = mountDetail(flag({ start_ms: 22000, end_ms: 27000 }));
    await w.get("[data-testid='fd-instruction']").setValue("too fast");
    await w.get("[data-testid='fd-send-refine']").trigger("click");
    const note = w.emitted("send-refine")![0][0] as { time_range: unknown };
    expect(note.time_range).toEqual({ start_ms: 22000, end_ms: 27000 });
  });

  it("emits send-chat for a per-flag question", async () => {
    const w = mountDetail(flag());
    await w.get("[data-testid='fd-chat-box']").setValue("why is it small?");
    await w.get("[data-testid='fd-chat-send']").trigger("click");
    expect(w.emitted("send-chat")![0]).toEqual(["why is it small?"]);
  });

  it("shows the empty state when no flag is selected", () => {
    const w = mount(FlagDetail, {
      props: { flag: null, video: "v#1", chat: [] },
    });
    expect(w.find("[data-testid='fd-empty']").exists()).toBe(true);
  });
});
