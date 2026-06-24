/**
 * Unit tests for the operator-question Pinia store. The LiveSource is a fake
 * (no live server, no LLM): the store's job is to queue forwarded questions
 * FIFO, surface the head as the active modal, and round-trip the answer RPC,
 * de-duping replayed frames and advancing only on a successful answer.
 */

import { describe, it, expect, beforeEach, vi } from "vitest";
import { setActivePinia, createPinia } from "pinia";
import { useOperatorQuestionStore } from "../../src/stores/operatorQuestions.js";
import type {
  LiveSource,
  OperatorQuestionFrame,
} from "../../src/data/live-source.js";

function frame(id: string): OperatorQuestionFrame {
  return {
    session_id: "pub-1",
    question_id: id,
    questions: [{ question: "Ship?", header: "Ship", options: [{ label: "Yes" }] }],
  };
}

/** A fake source whose subscribeQuestions hands the store a push() driver. */
function fakeSource(
  answerImpl?: (id: string, a: Record<string, unknown>) => Promise<{ ok: boolean }>
): { source: LiveSource; push: (f: OperatorQuestionFrame) => void; answer: ReturnType<typeof vi.fn> } {
  let onFrame: ((f: OperatorQuestionFrame) => void) | null = null;
  const answer = vi.fn(answerImpl ?? (async () => ({ ok: true })));
  const source = {
    subscribeQuestions: (cb: (f: OperatorQuestionFrame) => void) => {
      onFrame = cb;
      return () => {
        onFrame = null;
      };
    },
    answerQuestion: answer,
  } as unknown as LiveSource;
  return { source, push: (f) => onFrame?.(f), answer };
}

describe("operator-question store", () => {
  beforeEach(() => setActivePinia(createPinia()));

  it("queues frames FIFO and exposes the head as active", () => {
    const store = useOperatorQuestionStore();
    const { source, push } = fakeSource();
    store.init(source);

    push(frame("q-1"));
    push(frame("q-2"));

    expect(store.pending).toBe(2);
    expect(store.active?.question_id).toBe("q-1");
  });

  it("de-dupes a replayed question_id", () => {
    const store = useOperatorQuestionStore();
    const { source, push } = fakeSource();
    store.init(source);

    push(frame("q-1"));
    push(frame("q-1")); // reconnect replay

    expect(store.pending).toBe(1);
  });

  it("answer round-trips the RPC and advances the queue on ok", async () => {
    const store = useOperatorQuestionStore();
    const { source, push, answer } = fakeSource();
    store.init(source);
    push(frame("q-1"));
    push(frame("q-2"));

    await store.answer({ Ship: "Yes" });

    expect(answer).toHaveBeenCalledWith("q-1", { Ship: "Yes" });
    expect(store.active?.question_id).toBe("q-2");
    expect(store.pending).toBe(1);
  });

  it("short-circuits the RPC for a demo- question_id and advances locally", async () => {
    const store = useOperatorQuestionStore();
    const { source, push, answer } = fakeSource();
    store.init(source);
    push(frame("demo-q-1"));
    push(frame("q-2"));

    await store.answer({ Ship: "Yes" });

    // No backend round-trip for the injected demo frame…
    expect(answer).not.toHaveBeenCalled();
    // …but the queue still advances and submitting toggled back off.
    expect(store.active?.question_id).toBe("q-2");
    expect(store.pending).toBe(1);
    expect(store.submitting).toBe(false);
  });

  it("leaves the question at the head when the answer RPC reports !ok", async () => {
    const store = useOperatorQuestionStore();
    const { source, push } = fakeSource(async () => ({ ok: false }));
    store.init(source);
    push(frame("q-1"));

    await store.answer({ Ship: "Yes" });

    expect(store.active?.question_id).toBe("q-1");
    expect(store.pending).toBe(1);
  });

  it("init is idempotent and teardown stops the feed", () => {
    const store = useOperatorQuestionStore();
    const { source, push } = fakeSource();
    const spy = vi.spyOn(source, "subscribeQuestions");
    store.init(source);
    store.init(source); // second init is a no-op
    expect(spy).toHaveBeenCalledTimes(1);

    store.teardown();
    push(frame("q-1")); // feed detached — nothing enqueues
    expect(store.pending).toBe(0);
  });
});
