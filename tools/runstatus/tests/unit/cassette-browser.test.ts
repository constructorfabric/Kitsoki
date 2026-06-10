import { describe, it, expect, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import CassetteBrowser from "../../src/components/editor/CassetteBrowser.vue";

function fakeSource(episodes: unknown[]) {
  return {
    editorCassettes: vi.fn().mockResolvedValue(episodes),
  } as never;
}

describe("CassetteBrowser", () => {
  const eps = [
    {
      cassette_file: "/s/flows/h.cassette.yaml",
      episode_id: "ep1",
      input_digest: "prompt digest…",
      output_preview: "recorded output",
    },
  ];

  it("lists episodes and expands on select, emitting the episode", async () => {
    const w = mount(CassetteBrowser, {
      props: {
        source: fakeSource(eps),
        storyPath: "/s/app.yaml",
        cassetteKey: { handler: "host.oracle.decide", phase: "clarifying" },
      },
    });
    await flushPromises();
    const items = w.findAll('[data-testid="editor-cassette-item"]');
    expect(items.length).toBe(1);
    expect(items[0].text()).toContain("ep1");
    expect(items[0].text()).toContain("prompt digest");

    await items[0].trigger("click");
    expect(w.find('[data-testid="editor-cassette-expand"]').text()).toContain(
      "recorded output"
    );
    expect(w.emitted("select")?.[0]?.[0]).toMatchObject({ episode_id: "ep1" });
    w.unmount();
  });

  it("shows a no-match note when empty", async () => {
    const w = mount(CassetteBrowser, {
      props: {
        source: fakeSource([]),
        storyPath: "/s/app.yaml",
        cassetteKey: { handler: "x", phase: "y" },
      },
    });
    await flushPromises();
    expect(w.text()).toContain("No matching cassette episodes");
    w.unmount();
  });
});
