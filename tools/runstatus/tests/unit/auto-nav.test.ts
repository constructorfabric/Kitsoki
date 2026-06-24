/**
 * Unit tests for the home-screen auto-navigation guard (src/lib/auto-nav.ts).
 *
 * The guard decides whether "/" may redirect into the lone live session. It must
 * persist per-tab (sessionStorage, survives reload) and degrade safely when
 * storage is unavailable — returning "already done" so the user is never trapped
 * in a redirect loop with no way to reach the stories list.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { autoNavDone, markAutoNavDone } from "../../src/lib/auto-nav.js";

describe("auto-nav guard", () => {
  beforeEach(() => {
    sessionStorage.clear();
  });

  it("starts unset and flips to done after marking", () => {
    expect(autoNavDone()).toBe(false);
    markAutoNavDone();
    expect(autoNavDone()).toBe(true);
  });

  it("persists the mark across reads (survives a reload within the tab)", () => {
    markAutoNavDone();
    // A reload re-imports the module but reads the same sessionStorage.
    expect(autoNavDone()).toBe(true);
  });

  describe("when sessionStorage throws", () => {
    afterEach(() => {
      vi.restoreAllMocks();
    });

    it("reports done (degrade to always-show-home, never trap the user)", () => {
      vi.spyOn(sessionStorage, "getItem").mockImplementation(() => {
        throw new Error("storage disabled");
      });
      expect(autoNavDone()).toBe(true);
    });

    it("swallows write failures rather than throwing", () => {
      vi.spyOn(sessionStorage, "setItem").mockImplementation(() => {
        throw new Error("storage disabled");
      });
      expect(() => markAutoNavDone()).not.toThrow();
    });
  });
});
