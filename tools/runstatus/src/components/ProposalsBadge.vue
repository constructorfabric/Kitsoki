<template>
  <!-- Proposals badge: a count pill in the InteractiveView topbar, modelled on
       InboxBadge. Shows the number of queued mining/write-mode proposals; turns
       the attention (orange) colour when a write-mode opt-in is parked. Clicking
       it surfaces the head proposal in the SAME operator-question card the
       operator already answers (accept / refine / dismiss). Hidden when the
       queue is empty — the hide-when-zero contract the TUI badge also honours. -->
  <button
    v-if="proposals.count > 0"
    class="proposals-badge"
    data-testid="proposals-badge"
    :class="{ 'proposals-badge--attention': proposals.attention }"
    :data-attention="proposals.attention ? 'true' : 'false'"
    :data-count="proposals.count"
    :title="`${proposals.count} pending proposal${proposals.count === 1 ? '' : 's'} — click to review`"
    :aria-label="`Proposals, ${proposals.count} pending`"
    @click="review"
  >
    <span class="proposals-badge__glyph">📋</span>
    <span
      class="proposals-badge__count"
      data-testid="proposals-badge-count"
    >{{ proposals.count }}</span>
  </button>
</template>

<script setup lang="ts">
import { useProposalsStore } from "../stores/proposals.js";
import { useOperatorQuestionStore } from "../stores/operatorQuestions.js";
import type { OperatorQuestionFrame } from "../data/live-source.js";

const proposals = useProposalsStore();
const operatorQuestions = useOperatorQuestionStore();

// Clicking the badge surfaces the head proposal as an operator-question frame so
// the SAME modal renders the card (accept / refine / dismiss). The proposal's id
// rides as the frame's question_id, so the modal's submit resolves the exact
// proposal over the shared answer_question RPC — one gesture, one surface.
function review(): void {
  const p = proposals.active;
  if (!p) return;
  const header = p.kind === "write_mode" ? "May I edit?" : "Capture as structure?";
  const question = p.detail ? `${p.title}\n${p.detail}` : p.title;
  const frame: OperatorQuestionFrame = {
    session_id: "",
    question_id: p.id,
    questions: [
      {
        question,
        header,
        options: [
          { label: "accept" },
          { label: "refine" },
          { label: "dismiss" },
        ],
      },
    ],
  };
  // Pop the proposal off our queue (the modal now owns its resolution) and hand
  // it to the operator-question surface. De-dupe in onFrame keeps a double-click
  // from stacking it twice.
  operatorQuestions.onFrame(frame);
  proposals.queue.shift();
}
</script>

<style scoped>
.proposals-badge {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  height: 1.7rem;
  padding: 0 0.5rem;
  background: #1e293b;
  border: 1px solid #334155;
  border-radius: 999px;
  cursor: pointer;
  color: #e2e8f0;
  font: inherit;
  font-size: 0.78rem;
  transition: background 0.12s, border-color 0.12s;
}
.proposals-badge:hover {
  background: #273449;
}
.proposals-badge--attention {
  border-color: #fb923c;
  box-shadow: 0 0 0 2px rgba(251, 146, 60, 0.35);
}
.proposals-badge__glyph {
  font-size: 0.9rem;
  line-height: 1;
}
.proposals-badge__count {
  min-width: 1.05rem;
  height: 1.05rem;
  padding: 0 0.25rem;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: #1d4ed8;
  border-radius: 999px;
  font-size: 0.7rem;
  font-weight: 600;
  color: #fff;
}
.proposals-badge--attention .proposals-badge__count {
  background: #ea580c;
}
</style>
