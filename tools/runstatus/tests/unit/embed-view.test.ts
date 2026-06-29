/**
 * Unit tests for lib/embedView.ts — the host side of the generic `embed:view`
 * protocol an embedded artifact uses to report which place it is showing, so a
 * refine targets the slide the operator is looking at.
 */
import { describe, it, expect, vi } from "vitest";
import {
  parseEmbedView,
  installEmbedViewListener,
  parseEmbedPick,
  installEmbedPickListener,
  sendAnnotateMode,
} from "../../src/lib/embedView.js";

describe("parseEmbedView", () => {
  it("parses a well-formed embed:view message", () => {
    expect(
      parseEmbedView({ type: "embed:view", producer: "slidey", scope: "9", step: 2, label: "Cat Wrangling", count: 35 }),
    ).toEqual({ producer: "slidey", scope: "9", step: "2", label: "Cat Wrangling", count: 35 });
  });

  it("coerces a numeric scope to a string (opaque round-trip token)", () => {
    expect(parseEmbedView({ type: "embed:view", scope: 9 })?.scope).toBe("9");
  });

  it("ignores non-embed:view and malformed messages", () => {
    expect(parseEmbedView({ type: "slidey:scene", sceneIndex: 9 })).toBeNull();
    expect(parseEmbedView({ type: "embed:view" })).toBeNull(); // no scope
    expect(parseEmbedView({ type: "embed:view", scope: "" })).toBeNull();
    expect(parseEmbedView(null)).toBeNull();
    expect(parseEmbedView("embed:view")).toBeNull();
  });
});

describe("installEmbedViewListener", () => {
  it("invokes onView for embed:view messages and tears down cleanly", () => {
    const listeners: Record<string, (ev: Event) => void> = {};
    const target = {
      addEventListener: vi.fn((t: string, h: (ev: Event) => void) => (listeners[t] = h)),
      removeEventListener: vi.fn((t: string) => delete listeners[t]),
    };
    const seen: string[] = [];
    const teardown = installEmbedViewListener((v) => seen.push(v.scope), target as never);

    listeners.message({ data: { type: "embed:view", scope: "2", label: "B" } } as MessageEvent);
    listeners.message({ data: { type: "noise" } } as MessageEvent);
    listeners.message({ data: { type: "embed:view", scope: "9" } } as MessageEvent);
    expect(seen).toEqual(["2", "9"]);

    teardown();
    expect(target.removeEventListener).toHaveBeenCalled();
  });

  it("is a no-op without a target window", () => {
    expect(() => installEmbedViewListener(() => {}, undefined)()).not.toThrow();
  });
});

describe("parseEmbedPick", () => {
  it("parses a well-formed embed:pick with bbox", () => {
    expect(
      parseEmbedPick({ type: "embed:pick", producer: "slidey", scope: "9", ref: "9/src", label: "image", bbox: [1, 2, 3, 4] }),
    ).toEqual({ producer: "slidey", scope: "9", ref: "9/src", label: "image", bbox: [1, 2, 3, 4] });
  });

  it("requires a ref and drops a malformed bbox", () => {
    expect(parseEmbedPick({ type: "embed:pick", scope: "9" })).toBeNull();
    expect(parseEmbedPick({ type: "embed:pick", ref: "9/src", bbox: [1, 2, 3] })?.bbox).toBeUndefined();
    expect(parseEmbedPick({ type: "embed:view", ref: "x" })).toBeNull();
  });
});

describe("installEmbedPickListener", () => {
  it("invokes onPick for embed:pick messages", () => {
    const listeners: Record<string, (ev: Event) => void> = {};
    const target = {
      addEventListener: vi.fn((t: string, h: (ev: Event) => void) => (listeners[t] = h)),
      removeEventListener: vi.fn(),
    };
    const refs: string[] = [];
    installEmbedPickListener((p) => refs.push(p.ref), target as never);
    listeners.message({ data: { type: "embed:pick", ref: "9/src", scope: "9" } } as MessageEvent);
    listeners.message({ data: { type: "noise" } } as MessageEvent);
    expect(refs).toEqual(["9/src"]);
  });
});

describe("sendAnnotateMode", () => {
  it("posts the host→producer enable message into the target window", () => {
    const post = vi.fn();
    sendAnnotateMode({ postMessage: post }, true, { scope: "9", step: "2" });
    expect(post).toHaveBeenCalledWith(
      { type: "embed:annotate", enabled: true, scope: "9", step: "2" },
      "*",
    );
  });
  it("is a no-op without a target window", () => {
    expect(() => sendAnnotateMode(null, true)).not.toThrow();
  });
});
