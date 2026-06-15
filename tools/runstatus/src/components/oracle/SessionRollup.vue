<template>
  <div class="rollup" data-testid="agent-actions-rollup">
    <div v-if="groups.length === 0" class="rollup__empty">
      No agent actions in this run yet. Calls that produce a transcript_ref
      appear here grouped by turn.
    </div>

    <div v-for="g in groups" :key="g.key" class="rollup__group">
      <div class="rollup__group-header">
        <span class="rollup__turn">Turn {{ g.turn }}</span>
        <span class="rollup__state">{{ g.state }}</span>
        <span class="rollup__group-count">{{ g.calls.length }} call{{ g.calls.length === 1 ? '' : 's' }}</span>
      </div>

      <div
        v-for="c in g.calls"
        :key="c.callId"
        class="rollup__call"
        data-testid="agent-actions-rollup-call"
      >
        <button class="rollup__call-head" @click="toggle(c.callId)">
          <span class="rollup__caret">{{ open.has(c.callId) ? '▾' : '▸' }}</span>
          <span class="rollup__verb" :class="'rollup__verb--' + c.verb">{{ c.verb || 'call' }}</span>
          <span class="rollup__call-id">{{ c.callId }}</span>
          <span class="rollup__events">{{ c.events }} action{{ c.events === 1 ? '' : 's' }}</span>
          <span v-if="c.isError" class="rollup__err">ERR</span>
        </button>

        <div v-if="open.has(c.callId)" class="rollup__call-body">
          <div v-if="c.loading" class="rollup__status">Loading…</div>
          <div v-else-if="c.error" class="rollup__status rollup__status--err">{{ c.error }}</div>
          <AgentActions v-else-if="c.data" :data="c.data" />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive } from "vue";
import type { TraceEvent } from "../../types.js";
import AgentActions from "./AgentActions.vue";
import { createDataSource } from "../../data/source.js";
import type { TranscriptData } from "../../data/transcript.js";

const props = defineProps<{
  events: TraceEvent[];
  sessionId?: string;
}>();

interface FetchState {
  loading: boolean;
  error: string;
  data: TranscriptData | null;
}

interface CallRow {
  callId: string;
  verb: string;
  events: number;
  isError: boolean;
  turn: number;
  state: string;
  loading: boolean;
  error: string;
  data: TranscriptData | null;
}

// Lazy per-call fetch state keyed by call_id; reactive so the row re-renders
// when its transcript lands. Kept separate from the derived row list so the
// (cheap) regrouping computed stays pure.
const fetched = reactive(new Map<string, FetchState>());
const open = reactive(new Set<string>());

interface Group {
  key: string;
  turn: number;
  state: string;
  calls: CallRow[];
}

// Every oracle call (complete OR error) carrying a transcript_ref becomes a row,
// de-duped by the deterministic call_id and grouped under its turn — the kitsoki
// analog of a session replay's "all agent actions" list. Fetched transcript
// state is merged in so an opened row shows its drawer.
const groups = computed<Group[]>(() => {
  const seen = new Set<string>();
  const byTurn = new Map<number, Group>();
  for (const ev of props.events) {
    const a = ev.attrs ?? {};
    if (ev.msg !== "oracle.call.complete" && ev.msg !== "oracle.call.error") continue;
    const ref = a.transcript_ref as { events?: number } | undefined;
    if (!ref || typeof ref !== "object") continue;
    const callId = typeof a.call_id === "string" ? a.call_id : "";
    if (!callId || seen.has(callId)) continue;
    seen.add(callId);
    const turn = ev.turn ?? 0;
    const f = fetched.get(callId);
    const row: CallRow = {
      callId,
      verb: typeof a.verb === "string" ? a.verb : "",
      events: typeof ref.events === "number" ? ref.events : 0,
      isError: ev.msg === "oracle.call.error",
      turn,
      state: ev.state_path ?? "",
      loading: f?.loading ?? false,
      error: f?.error ?? "",
      data: f?.data ?? null,
    };
    if (!byTurn.has(turn)) {
      byTurn.set(turn, { key: String(turn), turn, state: ev.state_path ?? "", calls: [] });
    }
    byTurn.get(turn)!.calls.push(row);
  }
  return Array.from(byTurn.values()).sort((x, y) => x.turn - y.turn);
});

async function toggle(callId: string): Promise<void> {
  if (open.has(callId)) {
    open.delete(callId);
    return;
  }
  open.add(callId);
  const existing = fetched.get(callId);
  if (existing?.data) return;

  const state: FetchState = existing ?? { loading: false, error: "", data: null };
  state.loading = true;
  state.error = "";
  fetched.set(callId, state);
  try {
    state.data = await createDataSource().getTranscript(props.sessionId || "", callId);
  } catch (e) {
    state.error = e instanceof Error ? e.message : String(e);
  } finally {
    state.loading = false;
  }
}
</script>

<style scoped>
.rollup {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
  overflow-y: auto;
  height: 100%;
  min-height: 0;
  padding: 0.3rem;
}

.rollup__empty {
  color: var(--k-fg-subtle, #475569);
  font-size: 0.8rem;
  padding: 0.6rem;
}

.rollup__group {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}

.rollup__group-header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.2rem 0.4rem;
  background: var(--k-bg-widget, #0f172a);
  border-bottom: 1px solid var(--k-border, #1e293b);
  position: sticky;
  top: 0;
  z-index: 1;
}

.rollup__turn {
  font-size: 0.7rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--k-fg-muted, #64748b);
}

.rollup__state {
  font-family: ui-monospace, monospace;
  font-size: 0.66rem;
  color: var(--k-fg-subtle, #475569);
}

.rollup__group-count {
  margin-left: auto;
  font-size: 0.66rem;
  color: var(--k-fg-subtle, #475569);
}

.rollup__call {
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 4px;
  overflow: hidden;
}

.rollup__call-head {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 0.45rem;
  background: var(--k-bg-inset, #0a1728);
  border: none;
  cursor: pointer;
  padding: 0.3rem 0.5rem;
  font-size: 0.74rem;
  text-align: left;
}

.rollup__call-head:hover {
  background: var(--k-bg-hover, #0f1e38);
}

.rollup__caret {
  color: var(--k-fg-muted, #64748b);
  font-size: 0.65rem;
}

.rollup__verb {
  padding: 0.05rem 0.35rem;
  border-radius: 3px;
  font-size: 0.62rem;
  font-weight: 700;
  font-family: ui-monospace, monospace;
  text-transform: uppercase;
  background: var(--k-bg-input, #1e293b);
  color: #94a3b8;
}

.rollup__verb--decide   { background: #1e1b4b; color: #a5b4fc; }
.rollup__verb--task     { background: #450a0a; color: #fca5a5; }
.rollup__verb--ask      { background: #431407; color: #fdba74; }
.rollup__verb--extract  { background: #042f2e; color: #5eead4; }
.rollup__verb--converse { background: #083344; color: #67e8f9; }

.rollup__call-id {
  font-family: ui-monospace, monospace;
  font-size: 0.68rem;
  color: var(--k-fg-code, #7dd3fc);
}

.rollup__events {
  margin-left: auto;
  font-size: 0.66rem;
  color: var(--k-fg-muted, #64748b);
}

.rollup__err {
  background: #7f1d1d;
  color: var(--k-error, #fca5a5);
  font-size: 0.6rem;
  padding: 0.05rem 0.25rem;
  border-radius: 2px;
}

.rollup__call-body {
  padding: 0.4rem 0.5rem;
  background: var(--k-bg-inset, #060b14);
  border-top: 1px solid var(--k-border, #1e293b);
}

.rollup__status {
  color: var(--k-fg-muted, #64748b);
  font-size: 0.74rem;
}

.rollup__status--err {
  color: var(--k-error, #fca5a5);
}
</style>
