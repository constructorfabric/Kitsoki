import { describe, it, expect } from "vitest";
import { mount } from "@vue/test-utils";
import HookDetail from "../../src/components/editor/HookDetail.vue";

describe("HookDetail", () => {
  it("renders one card per on_enter effect with kind badge + fields", () => {
    const w = mount(HookDetail, {
      props: {
        onEnter: [
          { kind: "invoke", invoke: "host.oracle.decide", id: "pick", bind: ["choice"] },
          { kind: "set", sets: ["greeted"], when: "world.first" },
        ],
      },
    });
    const cards = w.findAll('[data-testid="editor-hook-effect"]');
    expect(cards.length).toBe(2);
    expect(cards[0].text()).toContain("invoke");
    expect(cards[0].text()).toContain("host.oracle.decide");
    expect(cards[0].text()).toContain("choice");
    expect(cards[1].text()).toContain("set");
    expect(cards[1].text()).toContain("greeted");
    expect(cards[1].text()).toContain("world.first");
    w.unmount();
  });

  it("shows an empty note when no effects", () => {
    const w = mount(HookDetail, { props: { onEnter: [] } });
    expect(w.text()).toContain("no on_enter effects");
    w.unmount();
  });
});
