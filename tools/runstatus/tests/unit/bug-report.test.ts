/**
 * Component tests for the "Report bug" surface in MetaButton. The new flow is
 * capture → review modal → submit, so the launcher item now kicks off the
 * store's trigger() action (capture) rather than fire-and-forget filing. The
 * action is spied to drive states deterministically (no html2canvas, no RPC).
 * We assert the capturing toast, the post-submit filed toast, the error toast,
 * and that the launcher is absent in snapshot mode.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { mount, flushPromises } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";
import MetaButton from "../../src/components/meta/MetaButton.vue";
import { setEmbeddedOverride } from "../../src/lib/embed.js";
import { useBugReportStore } from "../../src/stores/bugReport.js";

// MetaButton uses useRoute(); provide a minimal stub so mounting works without
// a real router.
vi.mock("vue-router", () => ({
  useRoute: () => ({ params: { sessionId: "pub-1" } }),
}));

vi.mock("../../src/data/live-source.js", () => ({
  LiveSource: vi.fn().mockImplementation(() => ({
    metaModes: vi.fn().mockResolvedValue([]),
  })),
}));

function open(wrapper: ReturnType<typeof mount>) {
  return wrapper.get('[data-testid="meta-button"]').trigger("click");
}

describe("MetaButton — Report bug", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    delete (globalThis as Record<string, unknown>).__KITSOKI_SNAPSHOT__;
  });
  afterEach(() => {
    delete (globalThis as Record<string, unknown>).__KITSOKI_SNAPSHOT__;
    setEmbeddedOverride(null);
  });

  it("clicking Report bug kicks off capture (trigger) and shows the capturing toast", async () => {
    const store = useBugReportStore();
    // Stub trigger() so it ends in the capturing state (modal would open on
    // reviewing; here we just assert capture started).
    const trigger = vi.spyOn(store, "trigger").mockImplementation(async () => {
      store.status = "capturing";
    });

    const wrapper = mount(MetaButton);
    await open(wrapper);
    await wrapper.get('[data-testid="meta-report-bug"]').trigger("click");
    await flushPromises();

    expect(trigger).toHaveBeenCalledTimes(1);
    expect(
      wrapper.get('[data-testid="bug-toast-capturing"]').exists()
    ).toBe(true);
  });

  it("shows the filed toast with the path after a successful submit", async () => {
    const store = useBugReportStore();
    vi.spyOn(store, "trigger").mockResolvedValue();
    // Simulate the post-submit filed state the modal would produce.
    store.filed = {
      id: "2026-06-12T130405Z-foyer-button-does-nothing",
      path: "issues/bugs/2026-06-12T130405Z-foyer-button-does-nothing.md",
    };
    store.status = "filed";

    const wrapper = mount(MetaButton);
    await flushPromises();

    expect(wrapper.get('[data-testid="bug-toast-path"]').text()).toContain(
      "issues/bugs/2026-06-12T130405Z-foyer-button-does-nothing.md"
    );
    expect(wrapper.find('[data-testid="bug-toast-open"]').exists()).toBe(true);
  });

  it("surfaces an error state in the toast when filing fails", async () => {
    const store = useBugReportStore();
    vi.spyOn(store, "trigger").mockImplementation(async () => {
      store.status = "error";
      store.error = "write failed";
    });

    const wrapper = mount(MetaButton);
    await open(wrapper);
    await wrapper.get('[data-testid="meta-report-bug"]').trigger("click");
    await flushPromises();

    expect(wrapper.get('[data-testid="bug-toast-error"]').text()).toContain(
      "write failed"
    );
  });

  it("hides the Report bug item (whole launcher) in snapshot mode", () => {
    (globalThis as Record<string, unknown>).__KITSOKI_SNAPSHOT__ = {};
    const wrapper = mount(MetaButton);
    expect(wrapper.find('[data-testid="meta-launcher"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="meta-report-bug"]').exists()).toBe(false);
  });

  it("keeps the normal web launcher floating", () => {
    setEmbeddedOverride(false);
    const wrapper = mount(MetaButton);
    const launcher = wrapper.get('[data-testid="meta-launcher"]');
    expect(launcher.attributes("data-placement")).toBe("floating");
    expect(launcher.classes()).toContain("meta-launcher--floating");
  });

  it("suppresses the global floating launcher inside the VS Code embed", () => {
    setEmbeddedOverride(true);
    const wrapper = mount(MetaButton);
    expect(wrapper.find('[data-testid="meta-launcher"]').exists()).toBe(false);
  });

  it("allows the VS Code embed to render the topbar launcher variant", () => {
    setEmbeddedOverride(true);
    const wrapper = mount(MetaButton, { props: { placement: "topbar" } });
    const launcher = wrapper.get('[data-testid="meta-launcher"]');
    expect(launcher.attributes("data-placement")).toBe("topbar");
    expect(launcher.classes()).toContain("meta-launcher--topbar");
  });
});
