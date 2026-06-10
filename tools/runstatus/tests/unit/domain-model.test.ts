import { describe, it, expect } from "vitest";
import { mount } from "@vue/test-utils";
import DomainModel from "../../src/components/editor/DomainModel.vue";

describe("DomainModel", () => {
  it("renders world keys with direction, intents, transitions", () => {
    const w = mount(DomainModel, {
      props: {
        worldKeys: [{ name: "idea", type: "string", direction: "readwrite" }],
        intents: [{ name: "submit", title: "Submit" }],
        transitions: [{ intent: "submit", target: "brief" }],
      },
    });
    expect(w.find('[data-testid="editor-domain-worldkey"]').text()).toContain("idea");
    expect(w.find('[data-testid="editor-domain-worldkey"]').text()).toContain("readwrite");
    expect(w.find('[data-testid="editor-domain-intent"]').text()).toContain("submit");
    expect(w.find('[data-testid="editor-domain-transition"]').text()).toContain("brief");
    w.unmount();
  });

  it("emits select-room when a transition target is clicked", async () => {
    const w = mount(DomainModel, {
      props: {
        worldKeys: [],
        intents: [],
        transitions: [{ intent: "go", target: "clarifying" }],
      },
    });
    await w.find('[data-testid="editor-domain-transition-target"]').trigger("click");
    expect(w.emitted("select-room")?.[0]).toEqual(["clarifying"]);
    w.unmount();
  });

  it("does not link synthetic exit targets", () => {
    const w = mount(DomainModel, {
      props: {
        worldKeys: [],
        intents: [],
        transitions: [{ intent: "done", target: "@exit:done" }],
      },
    });
    expect(w.find('[data-testid="editor-domain-transition-target"]').exists()).toBe(false);
    expect(w.find('[data-testid="editor-domain-transition"]').text()).toContain("@exit:done");
    w.unmount();
  });
});
