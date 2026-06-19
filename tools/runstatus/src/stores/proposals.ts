import { defineStore } from "pinia";
import { computed, ref } from "vue";
import type { LiveSource } from "../data/live-source.js";

/**
 * The proposals store — the web mirror of the TUI proposals inbox (slice 4 of
 * the ad-hoc-workbench epic). It is the single inbox for BOTH proposal kinds:
 *
 *   - "structure"  — a mining-loop "capture this as intent/room/binding/gate?"
 *                    proposal. Non-blocking: no agent is parked.
 *   - "write_mode" — a write-mode opt-in ("may I make this edit?"). The agent
 *                    is parked mid-turn waiting on the verdict, so it drives the
 *                    badge's attention (orange) variant.
 *
 * Modelled on operatorQuestions.ts (FIFO queue, count, head-is-active) so the
 * card surface and the resolve RPC are shared. The deterministic
 * window.__pushProposal seam (mirroring __pushOperatorQuestion) lets a no-LLM
 * demo/spec seed the queue without a real miner. resolve() rides the existing
 * runstatus.session.answer_question RPC — the same gesture the operator already
 * knows — so the surface learns ONE verdict path.
 */

/** One queued proposal. Mirrors the TUI MineProposal. */
export interface Proposal {
  /** Stable id; the verdict echoes it back so the backend resolves the right one. */
  id: string;
  /** "structure" | "write_mode" — drives the card header and the attention variant. */
  kind: "structure" | "write_mode";
  /** Short headline shown on the card. */
  title: string;
  /** Optional body (the draft snippet). */
  detail?: string;
}

export const useProposalsStore = defineStore("proposals", () => {
  // ---- state ----
  // FIFO queue of pending proposals, oldest first (mirrors the TUI queue order).
  const queue = ref<Proposal[]>([]);
  // True while a resolve RPC is in flight (disables the card's submit button).
  const submitting = ref(false);

  let src: LiveSource | null = null;

  // ---- getters ----
  /** The head proposal (the one a badge click surfaces), or null when empty. */
  const active = computed<Proposal | null>(() => queue.value[0] ?? null);
  /** Queue depth — the badge count. */
  const count = computed(() => queue.value.length);
  /**
   * Attention (orange) variant: a parked write-mode opt-in is waiting. Structure
   * proposals are low-stakes/deferred and do NOT raise attention.
   */
  const attention = computed(() => queue.value.some((p) => p.kind === "write_mode"));

  // ---- actions ----

  /**
   * Bind the live source used to resolve proposals over answer_question.
   * Idempotent. Production also wires a proposals feed here once the runtime
   * sibling lands; until then the queue is fed by the demo seam.
   */
  function init(source: LiveSource): void {
    src = source;
  }

  function teardown(): void {
    src = null;
  }

  /** Enqueue a proposal unless its id is already queued (reconnect-safe). */
  function push(p: Proposal): void {
    if (!p.id) return;
    if (queue.value.some((q) => q.id === p.id)) return;
    queue.value.push(p);
  }

  /**
   * Resolve the head proposal with the operator's verdict and advance the queue.
   * verdict is one of "accept" | "refine" | "dismiss". The verdict rides the
   * existing answer_question RPC keyed by the proposal id (the runtime sibling
   * emits MiningProposalDecided / WriteModeGranted from it).
   *
   * Demo seam: a proposal pushed by the deterministic driver carries a "demo-"
   * id with no parked backend entry, so the RPC would 404 — for those we resolve
   * locally so the card dismisses exactly as in production (mirrors the
   * operatorQuestions demo short-circuit).
   */
  async function resolve(verdict: "accept" | "refine" | "dismiss"): Promise<void> {
    const p = active.value;
    if (!p || submitting.value) return;
    submitting.value = true;
    try {
      if (p.id.startsWith("demo-")) {
        queue.value.shift();
        return;
      }
      if (!src) return;
      const res = await src.answerQuestion(p.id, { [p.title]: verdict });
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
    count,
    attention,
    // actions
    init,
    teardown,
    push,
    resolve,
  };
});
