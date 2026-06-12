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

function fakeMetaStream(
  result: Record<string, unknown> = {},
  events: Array<{ type: string; text?: string; tool?: string; preview?: string }> = []
) {
  return vi.fn().mockImplementation(
    async (
      _sid: string,
      _mode: string,
      _chatId: string,
      _input: string,
      onEvent: (ev: { type: string; text?: string; tool?: string; preview?: string }) => void
    ) => {
      for (const ev of events) onEvent(ev);
      return {
        assistant: "hello",
        chat_id: "c1",
        reload_requested: false,
        changed_files: [],
        ...result,
      };
    }
  );
}

function fakeSource(overrides: Record<string, unknown> = {}): LiveSource {
  return {
    metaModes: vi.fn().mockResolvedValue([
      { key: "story.ask", label: "Story Q&A", banner: "", agent: "story-explainer", read_only: true, group: "story" },
    ]),
    metaEnter: vi.fn().mockResolvedValue({ chat_id: "c1", mode_key: "story.ask", messages: [] }),
    metaStream: fakeMetaStream(),
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
      metaStream: fakeMetaStream({
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

  it("delta events accumulate into pendingAssistantText during streaming", async () => {
    const meta = useMetaStore();
    const captured: string[] = [];
    const src = fakeSource({
      metaStream: fakeMetaStream({ assistant: "hello world" }, [
        { type: "delta", text: "hello " },
        { type: "delta", text: "world" },
      ]),
    });
    // Intercept pendingAssistantText changes during send
    const origSend = src.metaStream as ReturnType<typeof vi.fn>;
    origSend.mockImplementation(
      async (_s: string, _m: string, _c: string, _i: string, onEvent: (ev: { type: string; text?: string }) => void) => {
        onEvent({ type: "delta", text: "hello " });
        captured.push(meta.pendingAssistantText);
        onEvent({ type: "delta", text: "world" });
        captured.push(meta.pendingAssistantText);
        return { assistant: "hello world", chat_id: "c1", reload_requested: false, changed_files: [] };
      }
    );
    await meta.openMode(src, "s1", "story.ask");
    await meta.send(src, "hi");
    expect(captured[0]).toBe("hello ");
    expect(captured[1]).toBe("hello world");
    // Cleared after done
    expect(meta.pendingAssistantText).toBe("");
  });

  it("tool events accumulate into the pendingStream feed and survive on the message", async () => {
    const meta = useMetaStore();
    const src = fakeSource({
      metaStream: fakeMetaStream({ assistant: "done" }, [
        { type: "tool", tool: "Read", preview: "app.yaml" },
        { type: "tool", tool: "Edit", preview: "rooms/idle.yaml" },
      ]),
    });
    await meta.openMode(src, "s1", "story.edit");
    await meta.send(src, "make it darker");
    // The live feed is cleared on done…
    expect(meta.pendingStream).toEqual([]);
    // …but preserved on the finished assistant message (collapsed activity).
    const last = meta.activeTranscript[meta.activeTranscript.length - 1];
    expect(last.stream).toEqual([
      { kind: "tool", tool: "Read", preview: "app.yaml" },
      { kind: "tool", tool: "Edit", preview: "rooms/idle.yaml" },
    ]);
  });

  it("think frames render into the feed immediately; the reply narration is dropped", async () => {
    const meta = useMetaStore();
    const src = fakeSource({
      metaStream: fakeMetaStream({ assistant: "the final answer" }, [
        { type: "think", text: "Let me look at the story first." },
        { type: "tool", tool: "Read", preview: "app.yaml" },
        // The reply streams as narration chunks (stub-style fragments)…
        { type: "delta", text: "the " },
        { type: "delta", text: "final " },
        { type: "delta", text: "answer" },
      ]),
    });
    await meta.openMode(src, "s1", "story.ask");
    await meta.send(src, "hi");
    const last = meta.activeTranscript[meta.activeTranscript.length - 1];
    // …and must NOT duplicate into the feed — done carries it as the reply.
    expect(last.text).toBe("the final answer");
    expect(last.stream).toEqual([
      { kind: "thinking", text: "Let me look at the story first." },
      { kind: "tool", tool: "Read", preview: "app.yaml" },
    ]);
  });

  it("narration followed by a tool call is proven intermediate and flushes into the feed", async () => {
    const meta = useMetaStore();
    const src = fakeSource({
      metaStream: fakeMetaStream({ assistant: "reply" }, [
        // A COMPLETE intermediate narration (claude emits one whole thought
        // per frame, no trailing whitespace)…
        { type: "delta", text: "I need to check the rooms." },
        // …proven intermediate by the tool round-trip that follows.
        { type: "tool", tool: "Grep", preview: "rooms/" },
        { type: "delta", text: "reply" },
      ]),
    });
    await meta.openMode(src, "s1", "story.ask");
    await meta.send(src, "hi");
    const last = meta.activeTranscript[meta.activeTranscript.length - 1];
    expect(last.stream).toEqual([
      { kind: "thinking", text: "I need to check the rooms." },
      { kind: "tool", tool: "Grep", preview: "rooms/" },
    ]);
    expect(last.text).toBe("reply");
  });

  it("a fresh complete narration flushes the previous one (TUI deferral parity)", async () => {
    const meta = useMetaStore();
    const src = fakeSource({
      metaStream: fakeMetaStream({ assistant: "second thought" }, [
        { type: "delta", text: "first thought" },
        { type: "delta", text: "second thought" },
      ]),
    });
    await meta.openMode(src, "s1", "story.ask");
    await meta.send(src, "hi");
    const last = meta.activeTranscript[meta.activeTranscript.length - 1];
    // The first complete narration was intermediate (a fresh one followed);
    // the second was the reply and stays out of the feed.
    expect(last.stream).toEqual([{ kind: "thinking", text: "first thought" }]);
  });

  it("pendingStream and pendingAssistantText are cleared on error", async () => {
    const meta = useMetaStore();
    const src = fakeSource({
      metaStream: vi.fn().mockImplementation(
        async (_s: string, _m: string, _c: string, _i: string, onEvent: (ev: { type: string; text?: string; tool?: string; preview?: string }) => void) => {
          onEvent({ type: "delta", text: "partial" });
          onEvent({ type: "tool", tool: "Read", preview: "x" });
          throw new Error("network error");
        }
      ),
    });
    await meta.openMode(src, "s1", "story.ask");
    await meta.send(src, "hi");
    expect(meta.pendingAssistantText).toBe("");
    expect(meta.pendingStream).toEqual([]);
    expect(meta.error).toContain("network error");
  });

  it("multi-round sends accumulate transcript correctly", async () => {
    const meta = useMetaStore();
    let callCount = 0;
    const src = fakeSource({
      metaStream: vi.fn().mockImplementation(
        async () => ({
          assistant: `reply ${++callCount}`,
          chat_id: "c1",
          reload_requested: false,
          changed_files: [],
        })
      ),
    });
    await meta.openMode(src, "s1", "story.ask");
    await meta.send(src, "first");
    await meta.send(src, "second");
    await meta.send(src, "third");
    expect(meta.activeTranscript).toHaveLength(6); // 3 user + 3 assistant
    expect(meta.activeTranscript[0]).toEqual({ role: "user", text: "first" });
    expect(meta.activeTranscript[1]).toEqual({ role: "assistant", text: "reply 1" });
    expect(meta.activeTranscript[4]).toEqual({ role: "user", text: "third" });
    expect(meta.activeTranscript[5]).toEqual({ role: "assistant", text: "reply 3" });
  });
});
