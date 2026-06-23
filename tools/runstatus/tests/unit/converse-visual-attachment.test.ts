/**
 * Spatial-attachment-trace slice 3 — the converse message render reads the
 * recorded `input.visual` block off the oracle call and shows a frame thumbnail
 * + an element chip; a call WITHOUT a visual block renders unchanged (the compat
 * case). No network: artifactUrl is a pure string builder, and the bundle is
 * synthetic (the same shape host.recordedVisualInput stamps on the trace).
 */

import { describe, it, expect } from "vitest";
import { mount } from "@vue/test-utils";
import ConverseDetail from "../../src/components/agent/ConverseDetail.vue";
import type { TraceEvent } from "../../src/types.js";

function converseEvent(input?: Record<string, unknown>): TraceEvent {
  return {
    time: "2026-06-17T18:22:04Z",
    level: "info",
    session_id: "sess-1",
    state_path: "root.offpath",
    turn: 1,
    msg: "agent.call.complete",
    attrs: {
      call_id: "2d8e4fbb0a78646d",
      verb: "converse",
      model: "claude-x",
      duration_ms: 4200,
      ...(input ? { input } : {}),
    },
  } as TraceEvent;
}

const visualBundle = {
  schema_version: 1,
  frame_handle: "frame#9f31abcd",
  media_handle: "art:media-7c02",
  point: { x: 1180, y: 540 },
  t_ms: 14300,
  route: "/review?video=art:media-7c02",
  element: {
    selector: "[data-testid=intent-btn-run]",
    role: "button",
    text: "Run",
    bbox: [1140, 520, 96, 40],
  },
};

describe("ConverseDetail — spatial attachment", () => {
  it("renders the frame thumbnail + element chip from the recorded input.visual", () => {
    const ev = converseEvent({
      messages: [{ role: "user", content: "why is this disabled here?" }],
      visual: visualBundle,
    });
    const w = mount(ConverseDetail, { props: { event: ev } });

    const attachment = w.find('[data-testid="visual-attachment"]');
    expect(attachment.exists()).toBe(true);

    // Thumbnail rides by handle, downscaled (the heavy full-res is click-to-zoom).
    const thumb = w.find('[data-testid="visual-thumb"]');
    expect(thumb.exists()).toBe(true);
    const src = thumb.attributes("src") ?? "";
    // Handle is encoded into the path ('#' → %23) and carries the downscale hint.
    expect(src).toContain("/artifact/");
    expect(src).toContain(encodeURIComponent("frame#9f31abcd"));
    expect(src).toContain("max=");

    // The element chip summarises selector + role + text.
    const chip = w.find('[data-testid="visual-element-chip"]');
    expect(chip.exists()).toBe(true);
    expect(chip.text()).toContain("[data-testid=intent-btn-run]");
    expect(chip.text()).toContain("button");
    expect(chip.text()).toContain("Run");

    // The point coordinates render for grounding.
    expect(w.find('[data-testid="visual-point"]').text()).toContain("(1180, 540)");

    // The full bundle is shown for audit — including the bbox for re-overlay.
    const bundle = w.find('[data-testid="visual-bundle"]');
    expect(bundle.exists()).toBe(true);
    expect(bundle.text()).toContain("1140");
    expect(bundle.text()).toContain("schema_version");
  });

  it("renders unchanged when the call carries no visual block (compat case)", () => {
    const ev = converseEvent({
      messages: [{ role: "user", content: "plain question" }],
    });
    const w = mount(ConverseDetail, { props: { event: ev } });

    expect(w.find('[data-testid="visual-attachment"]').exists()).toBe(false);
    expect(w.find('[data-testid="visual-thumb"]').exists()).toBe(false);
    expect(w.find('[data-testid="visual-element-chip"]').exists()).toBe(false);
    // The message bubble still renders exactly as before.
    expect(w.text()).toContain("plain question");
  });

  it("degrades to frame + point when no element was resolved", () => {
    const ev = converseEvent({
      visual: {
        schema_version: 1,
        frame_handle: "frame#abc",
        point: { x: 10, y: 20 },
      },
    });
    const w = mount(ConverseDetail, { props: { event: ev } });

    expect(w.find('[data-testid="visual-attachment"]').exists()).toBe(true);
    expect(w.find('[data-testid="visual-thumb"]').exists()).toBe(true);
    // No element → no chip, but the point still grounds the attachment.
    expect(w.find('[data-testid="visual-element-chip"]').exists()).toBe(false);
    expect(w.find('[data-testid="visual-point"]').text()).toContain("(10, 20)");
  });
});
