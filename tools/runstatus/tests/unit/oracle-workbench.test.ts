import { describe, it, expect, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import OracleWorkbench from "../../src/components/editor/OracleWorkbench.vue";

vi.mock("../../src/data/source.js", () => ({
  createDataSource: () => ({ artifactUrl: (h: string) => `/fake/${h}` }),
}));

function fakeSource(over: Record<string, unknown> = {}) {
  return {
    editorOracles: vi.fn().mockResolvedValue({
      contracts: [
        {
          kind: "host.oracle.decide",
          prompt_path: "prompts/p.txt",
          output_schema: "schemas/s.json",
          cassette_key: { handler: "host.oracle.decide", phase: "clarifying", call: "pick" },
          effect_index: 0,
        },
      ],
      cassette_globs: [],
    }),
    editorCassettes: vi.fn().mockResolvedValue([]),
    editorReplay: vi.fn().mockResolvedValue({
      output: { choice: "yes" },
      world_snapshot: { decision: "yes" },
      source: "cassette",
    }),
    ...over,
  } as never;
}

describe("OracleWorkbench", () => {
  it("renders a card per contract with verb + prompt + schema", async () => {
    const w = mount(OracleWorkbench, {
      props: { source: fakeSource(), storyPath: "/s/app.yaml", roomId: "clarifying" },
    });
    await flushPromises();
    const card = w.find('[data-testid="editor-oracle-card"]');
    expect(card.exists()).toBe(true);
    expect(card.text()).toContain("host.oracle.decide");
    expect(card.text()).toContain("prompts/p.txt");
    expect(card.text()).toContain("schemas/s.json");
    w.unmount();
  });

  it("replays a cassette and emits the world snapshot", async () => {
    const w = mount(OracleWorkbench, {
      props: { source: fakeSource(), storyPath: "/s/app.yaml", roomId: "clarifying" },
    });
    await flushPromises();
    await w.find('[data-testid="editor-oracle-replay-cassette"]').trigger("click");
    await flushPromises();
    expect(w.find('[data-testid="editor-oracle-output"]').text()).toContain("yes");
    expect(w.emitted("replay")?.[0]?.[0]).toMatchObject({
      world: { decision: "yes" },
    });
    w.unmount();
  });

  it("disables the live replay button", async () => {
    const w = mount(OracleWorkbench, {
      props: { source: fakeSource(), storyPath: "/s/app.yaml", roomId: "clarifying" },
    });
    await flushPromises();
    const live = w.find('[data-testid="editor-oracle-replay-live"]');
    expect((live.element as HTMLButtonElement).disabled).toBe(true);
    w.unmount();
  });
});
