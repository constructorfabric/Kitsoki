/**
 * Component tests for MetaOverlay.vue. The LiveSource it constructs is mocked
 * (no server, no LLM). Teleport is stubbed to a passthrough so the modal's DOM
 * is queryable inside the wrapper.
 */

import { describe, it, expect, beforeEach, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { setActivePinia, createPinia } from "pinia";
import type { LiveSource } from "../../src/data/live-source.js";

function makeMetaStream(result: Record<string, unknown> = {}) {
  return vi.fn().mockImplementation(
    async (
      _sid: string,
      _mode: string,
      _chatId: string,
      _input: string,
      _onEvent: (ev: { type: string; text?: string }) => void
    ) => ({
      assistant: "ok",
      chat_id: "c1",
      reload_requested: false,
      changed_files: [],
      ...result,
    })
  );
}

const metaStream = makeMetaStream();
const metaEnter = vi.fn().mockResolvedValue({ chat_id: "c1", mode_key: "story.ask", messages: [] });
const metaNew = vi.fn().mockResolvedValue({ chat_id: "c2", mode_key: "story.ask", messages: [] });

vi.mock("../../src/data/live-source.js", () => ({
  LiveSource: vi.fn().mockImplementation(() => ({ metaStream, metaEnter, metaNew })),
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
    metaStream: makeMetaStream({ assistant: "hello", chat_id: "c1" }),
  } as unknown as LiveSource;
  meta.modes = [
    { key: "story.edit", label: "Story edit", banner: "Editing", agent: "story-author", read_only: false, group: "story" },
    { key: "story.ask", label: "Story Q&A", banner: "Ask away", agent: "story-explainer", read_only: true, group: "story" },
  ];
  await meta.openMode(src, "s1", "story.ask");
  await meta.send(src, "what state am I in?");
  return meta;
}

// renderText helper (mirrors the component's implementation for white-box tests)
function escapeHtml(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}
function renderText(src: string): string {
  return escapeHtml(src ?? "")
    .split("\n")
    .map((line) =>
      line
        .replace(/`([^`]+)`/g, "<code>$1</code>")
        .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
    )
    .join("\n");
}

describe("MetaOverlay", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    metaStream.mockReset();
    metaStream.mockImplementation(makeMetaStream());
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

    // The component's own (mocked) LiveSource.metaStream is invoked.
    expect(metaStream).toHaveBeenCalled();
    expect(metaStream.mock.calls[0]).toContain("another question");
    wrapper.unmount();
  });

  // ── Streaming bubble ──────────────────────────────────────────────────────

  // A LiveSource whose metaStream emits the given events then never resolves,
  // so the turn stays in flight (busy + live feed) for the component to inspect.
  // Mirrors how the app actually reaches the streaming state — busy/pendingStream
  // are per-scope runtime, not directly assignable from outside.
  function streamingSource(
    events: Array<{ type: string; text?: string; tool?: string; preview?: string }>
  ): LiveSource {
    return {
      metaEnter: vi.fn().mockResolvedValue({ chat_id: "c1", mode_key: "story.ask", messages: [] }),
      metaStream: vi.fn().mockImplementation(
        async (
          _s: string,
          _m: string,
          _c: string,
          _i: string,
          onEvent: (ev: { type: string; text?: string; tool?: string; preview?: string }) => void
        ) => {
          for (const ev of events) onEvent(ev);
          return new Promise(() => {}); // never resolves — the turn keeps streaming
        }
      ),
    } as unknown as LiveSource;
  }

  it("streaming bubble shows 🧠 agent label while busy", async () => {
    const meta = useMetaStore();
    meta.modes = [
      { key: "story.ask", label: "Story Q&A", banner: "", agent: "story-explainer", read_only: true, group: "story" },
    ];
    const src = streamingSource([{ type: "delta", text: "thinking..." }]);
    await meta.openMode(src, "s1", "story.ask");
    void meta.send(src, "hi"); // do not await — the turn stays in flight
    await flushPromises();

    const wrapper = mount(MetaOverlay, mountOpts);
    await flushPromises();

    const streaming = wrapper.find("[data-testid='meta-row-streaming']");
    expect(streaming.exists()).toBe(true);
    expect(streaming.find(".meta-row__who").text()).toContain("🧠");
    expect(streaming.find(".meta-row__text").text()).toContain("thinking...");
    wrapper.unmount();
  });

  it("streaming bubble renders the live activity feed (thoughts + tools, in order)", async () => {
    const meta = useMetaStore();
    meta.modes = [
      { key: "story.ask", label: "Story Q&A", banner: "", agent: "story-explainer", read_only: true, group: "story" },
    ];
    const src = streamingSource([
      { type: "think", text: "Let me look at the story first." },
      { type: "tool", tool: "Read", preview: "app.yaml" },
      { type: "tool", tool: "Glob", preview: "rooms/*.yaml" },
    ]);
    await meta.openMode(src, "s1", "story.ask");
    void meta.send(src, "hi"); // do not await — the turn stays in flight
    await flushPromises();

    const wrapper = mount(MetaOverlay, mountOpts);
    await flushPromises();

    // Same shared ActivityFeed rows as the main chat's thinking bubble.
    const rows = wrapper.findAll(".chat-activity__thought, .chat-activity__tool");
    expect(rows).toHaveLength(3);
    expect(rows[0].classes()).toContain("chat-activity__thought");
    expect(rows[0].text()).toContain("🧠");
    expect(rows[0].text()).toContain("Let me look at the story first.");
    expect(rows[1].text()).toContain("Read");
    expect(rows[1].text()).toContain("app.yaml");
    expect(rows[2].text()).toContain("Glob");
    expect(rows[2].text()).toContain("rooms/*.yaml");
    wrapper.unmount();
  });

  it("a finished message's feed renders collapsed and expands to the same rows", async () => {
    const meta = useMetaStore();
    meta.modes = [
      { key: "story.ask", label: "Story Q&A", banner: "", agent: "story-explainer", read_only: true, group: "story" },
    ];
    const src = {
      metaEnter: vi.fn().mockResolvedValue({
        chat_id: "c1",
        mode_key: "story.ask",
        messages: [
          { role: "user", text: "hi" },
          {
            role: "assistant",
            text: "the reply",
            stream: [
              { kind: "thinking", text: "Checking the story." },
              { kind: "tool", tool: "Read", preview: "app.yaml" },
            ],
          },
        ],
      }),
    } as unknown as LiveSource;
    await meta.openMode(src, "s1", "story.ask");

    const wrapper = mount(MetaOverlay, mountOpts);
    await flushPromises();

    const activity = wrapper.find("[data-testid='meta-activity']");
    expect(activity.exists()).toBe(true);
    expect(wrapper.find("[data-testid='meta-activity-summary']").text()).toBe(
      "🧠 1 thought · 1 tool call"
    );
    // Collapsed by default (no `open` attribute on the <details>).
    expect(activity.attributes("open")).toBeUndefined();
    // The feed rows are present in the same shared presentation.
    const rows = activity.findAll(".chat-activity__thought, .chat-activity__tool");
    expect(rows).toHaveLength(2);
    expect(rows[0].text()).toContain("Checking the story.");
    expect(rows[1].text()).toContain("Read");
    wrapper.unmount();
  });

  it("streaming bubble is hidden when not busy", async () => {
    await seedOpen();
    const wrapper = mount(MetaOverlay, mountOpts);
    await flushPromises();

    expect(wrapper.find("[data-testid='meta-row-streaming']").exists()).toBe(false);
    wrapper.unmount();
  });

  // ── renderText line separation and inline markdown ────────────────────────

  it("renderText preserves newlines for white-space: pre-wrap", () => {
    const result = renderText("line one\nline two\nline three");
    expect(result).toContain("line one\nline two\nline three");
  });

  it("renderText renders backtick code spans", () => {
    const result = renderText("use `code` here");
    expect(result).toContain("<code>code</code>");
  });

  it("renderText renders **bold** spans", () => {
    const result = renderText("this is **bold** text");
    expect(result).toContain("<strong>bold</strong>");
  });

  it("renderText escapes HTML to prevent XSS", () => {
    const result = renderText("<script>alert(1)</script>");
    expect(result).not.toContain("<script>");
    expect(result).toContain("&lt;script&gt;");
  });

  it("renderText handles multi-line reply with inline markdown", () => {
    const src = "You're at `idle`.\n\nNext: **submit** your intent.";
    const result = renderText(src);
    expect(result).toContain("<code>idle</code>");
    expect(result).toContain("<strong>submit</strong>");
    expect(result).toContain("\n\n");
  });

  it("committed agent messages use v-html with renderText (not plain text)", async () => {
    const meta = useMetaStore();
    const src = {
      metaEnter: vi.fn().mockResolvedValue({ chat_id: "c1", mode_key: "story.ask", messages: [] }),
      metaStream: vi.fn().mockResolvedValue({
        assistant: "You're at `idle` state.\n\nTry **submitting** now.",
        chat_id: "c1",
        reload_requested: false,
        changed_files: [],
      }),
    } as unknown as import("../../src/data/live-source.js").LiveSource;
    meta.modes = [
      { key: "story.ask", label: "Story Q&A", banner: "", agent: "story-explainer", read_only: true, group: "story" },
    ];
    await meta.openMode(src, "s1", "story.ask");
    await meta.send(src, "where am I?");

    const wrapper = mount(MetaOverlay, mountOpts);
    await flushPromises();

    const agentRow = wrapper.find("[data-testid='meta-row-agent']");
    expect(agentRow.exists()).toBe(true);
    // v-html renders the markup; innerHTML contains the rendered tags
    const html = agentRow.find(".meta-row__text").element.innerHTML;
    expect(html).toContain("<code>idle</code>");
    expect(html).toContain("<strong>submitting</strong>");
    wrapper.unmount();
  });
});
