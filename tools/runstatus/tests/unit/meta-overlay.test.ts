/**
 * Component tests for MetaOverlay.vue. The LiveSource it constructs is mocked
 * (no server, no LLM). Teleport is stubbed to a passthrough so the modal's DOM
 * is queryable inside the wrapper.
 */

import { describe, it, expect, beforeEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";
import type { LiveSource } from "../../src/data/live-source.js";

const metaSend = vi.fn();
const metaEnter = vi.fn().mockResolvedValue({ chat_id: "c1", mode_key: "story.ask", messages: [] });
const metaNew = vi.fn().mockResolvedValue({ chat_id: "c2", mode_key: "story.ask", messages: [] });

vi.mock("../../src/data/live-source.js", () => ({
  LiveSource: vi.fn().mockImplementation(() => ({ metaSend, metaEnter, metaNew })),
}));

import MetaOverlay from "../../src/components/meta/MetaOverlay.vue";
import { useMetaStore } from "../../src/stores/meta.js";

const mountOpts = {
  global: {
    stubs: {
      // Passthrough Teleport so the modal renders inside the wrapper.
      Teleport: { template: "<div><slot /></div>" },
    },
  },
};

// seed builds a fake source and drives the store into an open story.ask chat
// with one user + one agent message.
async function seedOpen() {
  const meta = useMetaStore();
  const src = {
    metaEnter: vi.fn().mockResolvedValue({ chat_id: "c1", mode_key: "story.ask", messages: [] }),
    metaSend: vi.fn().mockResolvedValue({ assistant: "hello", chat_id: "c1", reload_requested: false, changed_files: [] }),
  } as unknown as LiveSource;
  meta.modes = [
    { key: "story.edit", label: "Story edit", banner: "Editing", agent: "story-author", read_only: false, group: "story" },
    { key: "story.ask", label: "Story Q&A", banner: "Ask away", agent: "story-explainer", read_only: true, group: "story" },
  ];
  await meta.openMode(src, "s1", "story.ask");
  await meta.send(src, "what state am I in?");
  return meta;
}

describe("MetaOverlay", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    metaSend.mockReset();
    metaSend.mockResolvedValue({ assistant: "ok", chat_id: "c1", reload_requested: false, changed_files: [] });
  });

  it("does not render when the store is closed", () => {
    const wrapper = mount(MetaOverlay, mountOpts);
    expect(wrapper.find("[data-testid='meta-overlay']").exists()).toBe(false);
    wrapper.unmount();
  });

  it("renders the overlay, mode tabs, and the transcript when open", async () => {
    await seedOpen();
    const wrapper = mount(MetaOverlay, mountOpts);
    await flushPromises();

    expect(wrapper.find("[data-testid='meta-overlay']").exists()).toBe(true);
    expect(wrapper.find("[data-testid='meta-transcript']").exists()).toBe(true);
    expect(wrapper.find("[data-testid='meta-tab-story-edit']").exists()).toBe(true);
    expect(wrapper.find("[data-testid='meta-tab-story-ask']").exists()).toBe(true);
    expect(wrapper.find("[data-testid='meta-row-user']").text()).toContain("what state am I in?");
    expect(wrapper.find("[data-testid='meta-row-agent']").text()).toContain("hello");
    wrapper.unmount();
  });

  it("the close button closes the overlay", async () => {
    const meta = await seedOpen();
    const wrapper = mount(MetaOverlay, mountOpts);
    await flushPromises();

    await wrapper.find("[data-testid='meta-close']").trigger("click");
    expect(meta.open).toBe(false);
    wrapper.unmount();
  });

  it("Escape closes the overlay", async () => {
    const meta = await seedOpen();
    const wrapper = mount(MetaOverlay, mountOpts);
    await flushPromises();

    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" }));
    expect(meta.open).toBe(false);
    wrapper.unmount();
  });

  it("the composer sends the typed text", async () => {
    await seedOpen();
    const wrapper = mount(MetaOverlay, mountOpts);
    await flushPromises();

    await wrapper.find("[data-testid='meta-composer-input']").setValue("another question");
    await wrapper.find("[data-testid='meta-composer-send']").trigger("submit");
    await flushPromises();

    // The component's own (mocked) LiveSource.metaSend is invoked.
    expect(metaSend).toHaveBeenCalled();
    expect(metaSend.mock.calls[0]).toContain("another question");
    wrapper.unmount();
  });
});
