import { defineStore } from "pinia";
import { computed, ref } from "vue";
import type {
  LiveSource,
  OperatorQuestionFrame,
} from "../data/live-source.js";

/**
 * The operator-question store. App-global (mounted once in App.vue alongside the
 * inbox), so a forwarded question survives router navigation — a dispatched
 * agent blocks on the answer regardless of which room the operator is looking at.
 *
 * Feed model: the live feed (subscribeQuestions) is the only source. A frame
 * arrives when a dispatched agent forwards an AskUserQuestion into kitsoki (the
 * agent turn is parked, blocking, until we answer). Frames queue FIFO; the head
 * is surfaced as a blocking modal. answer() echoes the question_id back so the
 * backend can unblock the exact parked goroutine, then advances the queue.
 *
 * De-dupe by question_id: a reconnect could replay an unanswered frame.
 */
export const useOperatorQuestionStore = defineStore("operatorQuestions", () => {
  // ---- state ----
  // FIFO queue of unanswered forwarded questions. The head is the active modal.
  const queue = ref<OperatorQuestionFrame[]>([]);
  // True while an answer RPC is in flight (disables the submit button).
  const submitting = ref(false);

  let unsubscribe: (() => void) | null = null;
  let src: LiveSource | null = null;

  // ---- getters ----
  const active = computed<OperatorQuestionFrame | null>(
    () => queue.value[0] ?? null
  );
  const pending = computed(() => queue.value.length);

  // ---- actions ----

  /**
   * Start the forwarded-question feed. Idempotent: a second init() is a no-op so
   * App.vue can call it once on mount without double-subscribing.
   */
  function init(source: LiveSource): void {
    if (unsubscribe) return;
    src = source;
    unsubscribe = source.subscribeQuestions((frame) => onFrame(frame));
  }

  /** Tear down the feed (e.g. on app unmount / hot reload). */
  function teardown(): void {
    if (unsubscribe) {
      unsubscribe();
      unsubscribe = null;
    }
    src = null;
  }

  /** Handle one SSE push: enqueue unless we already hold this question_id. */
  function onFrame(frame: OperatorQuestionFrame): void {
    if (!frame.question_id) return;
    if (queue.value.some((q) => q.question_id === frame.question_id)) return;
    queue.value.push(frame);
  }

  /**
   * Answer the active question and advance the queue. answers is keyed by each
   * question's text → chosen label/custom answer (single) or labels
   * (multiSelect). On RPC failure the question is left at the head so the
   * operator can retry.
   */
  async function answer(
    answers: Record<string, string | string[]>
  ): Promise<void> {
    const frame = active.value;
    if (!frame || submitting.value) return;
    submitting.value = true;
    try {
      // Demo seam local-resolve: a frame injected by the deterministic demo
      // driver (window.__pushOperatorQuestion) has no pending registry entry on
      // the backend, so there is no parked goroutine to unblock — round-tripping
      // the answerQuestion RPC would 404. Frames the demo injects carry a
      // "demo-" question_id; for those we skip the RPC and resolve locally so the
      // modal dismisses exactly as it would in production. The modal rendering +
      // option-selection UX is unchanged; only the network round-trip is bypassed
      // (analogous to how __startTourWithSteps bypasses the tour auto-start).
      if (frame.question_id.startsWith("demo-")) {
        queue.value.shift();
        return;
      }
      if (!src) return;
      const res = await src.answerQuestion(frame.question_id, answers);
      if (res.ok) {
        queue.value.shift();
      }
    } finally {
      submitting.value = false;
    }
  }

  return {
    // state
    queue,
    submitting,
    // getters
    active,
    pending,
    // actions
    init,
    teardown,
    onFrame,
    answer,
  };
});
