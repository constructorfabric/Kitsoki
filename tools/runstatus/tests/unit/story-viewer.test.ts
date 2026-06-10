import { describe, it, expect, vi } from "vitest";
import { mount } from "@vue/test-utils";
import StoryViewer from "../../src/components/editor/StoryViewer.vue";

// StoryViewer must be self-contained: ViewElement.vue (its only child) calls
// createDataSource at module init, so we stub that — proving StoryViewer needs
// no store/session of its own.
vi.mock("../../src/data/source.js", () => ({
  createDataSource: () => ({ artifactUrl: (h: string) => `/fake/${h}` }),
}));

describe("StoryViewer", () => {
  it("renders view elements + world snapshot in column mode", () => {
    const w = mount(StoryViewer, {
      props: {
        mode: "column",
        view: { Elements: [{ Kind: "prose", Source: "Hello editor." }] },
        worldSnapshot: { idea: "build a thing" },
      },
    });
    expect(w.find('[data-testid="editor-story-viewer"]').exists()).toBe(true);
    expect(w.find('[data-testid="editor-story-viewer-view"]').text()).toContain(
      "Hello editor."
    );
    expect(w.find('[data-testid="editor-story-viewer-world"]').text()).toContain(
      "idea"
    );
    w.unmount();
  });

  it("renders in modal mode and emits close", async () => {
    const w = mount(StoryViewer, {
      props: { mode: "modal", view: { Elements: [] } },
    });
    expect(w.find(".story-viewer--modal").exists()).toBe(true);
    await w.find('[data-testid="editor-story-viewer-close"]').trigger("click");
    expect(w.emitted("close")).toBeTruthy();
    w.unmount();
  });

  it("mounts with no active Pinia (uses no store)", () => {
    // No setActivePinia / no plugins: a component that touched a Pinia store
    // would throw "getActivePinia()" here. A clean mount proves self-containment.
    const w = mount(StoryViewer, {
      props: { mode: "column", view: { Elements: [] } },
    });
    expect(w.find('[data-testid="editor-story-viewer"]').exists()).toBe(true);
    w.unmount();
  });
});
