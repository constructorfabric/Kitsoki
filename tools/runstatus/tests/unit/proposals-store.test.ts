/**
 * Unit tests for the proposals Pinia store — the web mirror of the TUI
 * proposals inbox. The LiveSource is a fake (no live server, no LLM): the
 * store's job is to queue proposals FIFO, surface the head as active, expose
 * a count + attention getter for the badge, and resolve the verdict over the
 * shared answer_question RPC (short-circuiting demo- ids locally).
 */

import { describe, it, expect, beforeEach, vi } from "vitest";
import { setActivePinia, createPinia } from "pinia";
import { useProposalsStore } from "../../src/stores/proposals.js";
import type { Proposal } from "../../src/stores/proposals.js";
import type { LiveSource } from "../../src/data/live-source.js";

function structure(id: string): Proposal {
  return { id, kind: "structure", title: `Capture ${id}` };
}
function writeMode(id: string): Proposal {
  return { id, kind: "write_mode", title: `Edit ${id}?` };
}

function fakeSource(
  answerImpl?: (id: string, a: Record<string, unknown>) => Promise<{ ok: boolean }>
): { source: LiveSource; answer: ReturnType<typeof vi.fn> } {
  const answer = vi.fn(answerImpl ?? (async () => ({ ok: true })));
  const source = { answerQuestion: answer } as unknown as LiveSource;
  return { source, answer };
}

describe("proposals store", () => {
  beforeEach(() => setActivePinia(createPinia()));

  it("queues proposals FIFO and exposes the head + count", () => {
    const store = useProposalsStore();
    store.push(structure("p-1"));
    store.push(structure("p-2"));
    expect(store.count).toBe(2);
    expect(store.active?.id).toBe("p-1");
  });

  it("de-dupes a replayed id", () => {
    const store = useProposalsStore();
    store.push(structure("p-1"));
    store.push(structure("p-1"));
    expect(store.count).toBe(1);
  });

  it("raises attention only when a write-mode opt-in is queued", () => {
    const store = useProposalsStore();
    store.push(structure("p-1"));
    expect(store.attention).toBe(false);
    store.push(writeMode("p-2"));
    expect(store.attention).toBe(true);
  });

  it("resolve round-trips answer_question keyed by the proposal id and advances", async () => {
    const store = useProposalsStore();
    const { source, answer } = fakeSource();
    store.init(source);
    store.push(structure("p-1"));
    store.push(structure("p-2"));

    await store.resolve("accept");

    expect(answer).toHaveBeenCalledWith("p-1", { "Capture p-1": "accept" });
    expect(store.active?.id).toBe("p-2");
    expect(store.count).toBe(1);
  });

  it("short-circuits the RPC for a demo- id and advances locally", async () => {
    const store = useProposalsStore();
    const { source, answer } = fakeSource();
    store.init(source);
    store.push(structure("demo-p-1"));
    store.push(structure("p-2"));

    await store.resolve("dismiss");

    expect(answer).not.toHaveBeenCalled();
    expect(store.active?.id).toBe("p-2");
    expect(store.submitting).toBe(false);
  });

  it("leaves the proposal at the head when the RPC reports !ok", async () => {
    const store = useProposalsStore();
    const { source } = fakeSource(async () => ({ ok: false }));
    store.init(source);
    store.push(structure("p-1"));

    await store.resolve("accept");

    expect(store.active?.id).toBe("p-1");
    expect(store.count).toBe(1);
  });

  it("teardown detaches the source so resolve becomes a no-op", async () => {
    const store = useProposalsStore();
    const { source, answer } = fakeSource();
    store.init(source);
    store.teardown();
    store.push(structure("p-1"));

    await store.resolve("accept");
    expect(answer).not.toHaveBeenCalled();
    expect(store.count).toBe(1); // not advanced — no source to resolve against
  });
});
