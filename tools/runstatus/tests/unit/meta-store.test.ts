/**
 * Unit tests for the meta-mode Pinia store. The LiveSource is a fake (no live
 * server, no LLM): the store's job is transcript persistence per (session,
 * mode), the reload handshake, and new-chat reset.
 */

import { describe, it, expect, beforeEach, vi } from "vitest";
import { setActivePinia, createPinia } from "pinia";
import { useMetaStore } from "../../src/stores/meta.js";
import { useRunStore } from "../../src/stores/run.js";
import type { LiveSource } from "../../src/data/live-source.js";

function fakeSource(overrides: Record<string, unknown> = {}): LiveSource {
  return {
    metaModes: vi.fn().mockResolvedValue([
      { key: "story.ask", label: "Story Q&A", banner: "", agent: "story-explainer", read_only: true, group: "story" },
    ]),
    metaEnter: vi.fn().mockResolvedValue({ chat_id: "c1", mode_key: "story.ask", messages: [] }),
    metaSend: vi.fn().mockResolvedValue({ assistant: "hello", chat_id: "c1", reload_requested: false, changed_files: [] }),
    metaNew: vi.fn().mockResolvedValue({ chat_id: "c2", mode_key: "story.ask", messages: [] }),
    reloadSession: vi.fn().mockResolvedValue({ ok: true, prev_state_exists: true }),
    ...overrides,
  } as unknown as LiveSource;
}

describe("meta store", () => {
  beforeEach(() => setActivePinia(createPinia()));

  it("opens a mode, seeds the transcript, and records the chat id", async () => {
    const meta = useMetaStore();
    const src = fakeSource();
    await meta.openMode(src, "s1", "story.ask");

    expect(meta.open).toBe(true);
    expect(meta.activeMode).toBe("story.ask");
    expect(src.metaEnter).toHaveBeenCalledWith("s1", "story.ask", "");
    expect(meta.activeTranscript).toEqual([]);
  });

  it("send appends the user turn and the assistant reply", async () => {
    const meta = useMetaStore();
    const src = fakeSource();
    await meta.openMode(src, "s1", "story.ask");
    await meta.send(src, "what state am I in?");

    expect(meta.activeTranscript).toEqual([
      { role: "user", text: "what state am I in?" },
      { role: "assistant", text: "hello" },
    ]);
  });

  it("keeps the transcript across close + reopen (persistence)", async () => {
    const meta = useMetaStore();
    const src = fakeSource();
    await meta.openMode(src, "s1", "story.ask");
    await meta.send(src, "hi");
    meta.close();
    expect(meta.open).toBe(false);

    await meta.openMode(src, "s1", "story.ask");
    expect(meta.open).toBe(true);
    expect(meta.activeTranscript).toHaveLength(2);
    // metaEnter is NOT called again — the scope already has a chat id.
    expect(src.metaEnter).toHaveBeenCalledTimes(1);
  });

  it("keeps separate transcripts per session", async () => {
    const meta = useMetaStore();
    const src = fakeSource();
    await meta.openMode(src, "s1", "story.ask");
    await meta.send(src, "for s1");
    await meta.openMode(src, "s2", "story.ask");

    expect(meta.activeSessionId).toBe("s2");
    expect(meta.activeTranscript).toEqual([]); // s2 is a fresh scope
  });

  it("on reload_requested, reloads the session and rehydrates the run store", async () => {
    const meta = useMetaStore();
    const runStore = useRunStore();
    const rehydrate = vi.spyOn(runStore, "rehydrate").mockResolvedValue();
    const src = fakeSource({
      metaSend: vi.fn().mockResolvedValue({
        assistant: "applied + reloaded",
        chat_id: "c1",
        reload_requested: true,
        changed_files: ["meta-edits.log"],
      }),
      metaEnter: vi.fn().mockResolvedValue({ chat_id: "c1", mode_key: "story.edit", messages: [] }),
    });
    await meta.openMode(src, "s1", "story.edit");
    await meta.send(src, "make it dark");

    expect(src.reloadSession).toHaveBeenCalledWith("s1");
    expect(rehydrate).toHaveBeenCalledWith(src, "s1");
    expect(meta.reloadNote).toContain("meta-edits.log");
  });

  it("newChat resets the active transcript", async () => {
    const meta = useMetaStore();
    const src = fakeSource();
    await meta.openMode(src, "s1", "story.ask");
    await meta.send(src, "hi");
    expect(meta.activeTranscript).toHaveLength(2);

    await meta.newChat(src);
    expect(src.metaNew).toHaveBeenCalled();
    expect(meta.activeTranscript).toEqual([]);
  });

  it("loadModes populates the available modes", async () => {
    const meta = useMetaStore();
    const src = fakeSource();
    await meta.loadModes(src, "s1");
    expect(meta.modes).toHaveLength(1);
    expect(meta.modes[0].key).toBe("story.ask");
  });
});
