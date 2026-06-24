<template>
  <div class="agent-detail">
    <!-- Common header -->
    <div class="agent-detail__header">
      <span class="agent-detail__verb-badge" :class="verbBadgeClass">{{ verb }}</span>
      <span v-if="agent" class="agent-detail__meta">{{ agent }}</span>
      <span v-if="model" class="agent-detail__meta agent-detail__meta--model">{{ model }}</span>
      <span class="agent-detail__spacer" />
      <span v-if="durationMs !== undefined" class="agent-detail__stat">{{ fmtMs(durationMs) }}</span>
      <span v-if="promptTokens !== undefined" class="agent-detail__stat agent-detail__stat--tokens">
        in:{{ fmtTokens(promptTokens) }}
      </span>
      <span v-if="responseTokens !== undefined" class="agent-detail__stat agent-detail__stat--tokens">
        out:{{ fmtTokens(responseTokens) }}
      </span>
      <span
        v-if="cacheReadTokens"
        class="agent-detail__stat agent-detail__stat--tokens"
        title="Prompt tokens served from the cache (cache_read_input_tokens)"
      >
        cache:{{ fmtTokens(cacheReadTokens) }}
      </span>
      <span v-if="costStr" class="agent-detail__stat agent-detail__stat--cost">{{ costStr }}</span>
    </div>

    <!-- Error banner -->
    <div v-if="errorMsg" class="agent-detail__error">{{ errorMsg }}</div>

    <!-- Token usage breakdown: per-type counts straight from the trace's
         meta.usage object, plus the total cost the CLI reported. Per-type cost
         is not recorded (the CLI returns a single total_cost_usd), so only the
         total is shown — the UI never fabricates a per-type split. -->
    <table v-if="usageRows.length" class="agent-detail__usage">
      <thead>
        <tr>
          <th class="agent-detail__usage-th">Token usage</th>
          <th class="agent-detail__usage-th agent-detail__usage-th--num">tokens</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in usageRows" :key="r.label" :title="r.hint">
          <td class="agent-detail__usage-label">{{ r.label }}</td>
          <td class="agent-detail__usage-num">{{ fmtTokens(r.tokens) }}</td>
        </tr>
      </tbody>
      <tfoot>
        <tr class="agent-detail__usage-total">
          <td class="agent-detail__usage-label">Total tokens</td>
          <td class="agent-detail__usage-num">{{ fmtTokens(totalTokens) }}</td>
        </tr>
        <tr v-if="costStr" class="agent-detail__usage-cost">
          <td class="agent-detail__usage-label">Total cost</td>
          <td class="agent-detail__usage-num">{{ costStr }}</td>
        </tr>
      </tfoot>
    </table>

    <!-- Per-verb body -->
    <DecideDetail  v-if="verb === 'decide'"  :event="event" />
    <ExtractDetail v-else-if="verb === 'extract'" :event="event" />
    <AskDetail     v-else-if="verb === 'ask'"     :event="event" />
    <TaskDetail    v-else-if="verb === 'task'"    :event="event" />
    <ConverseDetail v-else-if="verb === 'converse'" :event="event" />

    <!-- Fallback: raw attrs dump for unknown verbs -->
    <div v-else class="agent-detail__fallback">
      <pre class="agent-detail__pre">{{ prettyJson(event.attrs) }}</pre>
    </div>

    <!-- ── Agent actions: rich sidecar-backed transcript for EVERY verb ──────
         Shown whenever the event carries a transcript_ref pointer. The old
         TaskDetail "Transcript" tab is a names-only rollup of attrs.tool_calls;
         this drawer is the full, byte-verbatim, all-verb view fetched lazily
         from the <call_id>.jsonl + .timings sidecars. -->
    <div v-if="transcriptRef" class="agent-detail__agent">
      <button
        class="agent-detail__agent-affordance"
        data-testid="agent-actions-affordance"
        @click="toggleDrawer"
      >
        <span class="agent-detail__agent-icon">▸</span>
        Agent actions ({{ transcriptEvents }})
      </button>

      <div v-if="drawerOpen" class="agent-detail__agent-drawer">
        <div v-if="loading" class="agent-detail__agent-status">Loading transcript…</div>
        <div v-else-if="loadError" class="agent-detail__agent-status agent-detail__agent-status--err">
          {{ loadError }}
        </div>
        <AgentActions
          v-else-if="transcript"
          :data="transcript"
          :live="liveTranscript"
          :rerunning="rerunning"
          :can-rerun="canRerun"
          @rerun="rerunLive"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from "vue";
import type { TraceEvent } from "../../types.js";
import { fmtMs, fmtTokens, fmtCost, prettyJson, readAgentUsage } from "./lib.js";
import DecideDetail from "./DecideDetail.vue";
import ExtractDetail from "./ExtractDetail.vue";
import AskDetail from "./AskDetail.vue";
import TaskDetail from "./TaskDetail.vue";
import ConverseDetail from "./ConverseDetail.vue";
import AgentActions from "./AgentActions.vue";
import { createDataSource } from "../../data/source.js";
import { LiveSource } from "../../data/live-source.js";
import type { TranscriptData } from "../../data/transcript.js";

const props = defineProps<{
  event: TraceEvent;
  /** Optional explicit live session id (falls back to event.session_id). */
  sessionId?: string;
}>();

const attrs = computed(() => props.event.attrs);

const verb = computed(() => {
  // Canonical: the verb lives in attrs.verb (engine emits agent.call.*).
  // Fall back to inferring from a legacy per-verb msg ("agent.<verb>.complete")
  // but never treat the canonical "call" token as a verb.
  const fromAttrs = typeof attrs.value.verb === "string" ? attrs.value.verb : "";
  if (fromAttrs) return fromAttrs;
  const m = props.event.msg.match(/^agent\.([a-z]+)\.complete$/);
  return m && m[1] !== "call" ? m[1]! : "";
});
const agent    = computed(() => String(attrs.value.agent ?? ""));
const model    = computed(() => String(attrs.value.model ?? ""));
const durationMs     = computed(() => attrs.value.duration_ms as number | undefined);
// Token usage + cost come from the canonical opaque transport meta
// (attrs.meta.usage / attrs.meta.cost_usd), with a fallback to the legacy flat
// fields so synthetic fixtures still render. See readAgentUsage.
const usage          = computed(() => readAgentUsage(attrs.value));
const promptTokens   = computed(() => usage.value.promptTokens);
const responseTokens = computed(() => usage.value.responseTokens);
const cacheReadTokens = computed(() => usage.value.cacheReadTokens);
const costStr  = computed(() => fmtCost(usage.value.costUsd));

// Per-type token rows for the expanded breakdown table. claude reports the
// input categories disjointly: `input_tokens` is fresh (uncached) input, while
// cache read / cache write are billed separately — so they sum to the full
// input side. Only rows the trace actually carries are shown.
const usageRows = computed(() => {
  const u = usage.value;
  const rows: { label: string; tokens: number; hint: string }[] = [];
  if (u.promptTokens !== undefined)
    rows.push({ label: "Input (uncached)", tokens: u.promptTokens, hint: "input_tokens — fresh prompt tokens billed at full rate" });
  if (u.cacheReadTokens)
    rows.push({ label: "Cache read", tokens: u.cacheReadTokens, hint: "cache_read_input_tokens — prompt tokens served from the cache" });
  if (u.cacheCreationTokens)
    rows.push({ label: "Cache write", tokens: u.cacheCreationTokens, hint: "cache_creation_input_tokens — prompt tokens written to the cache" });
  if (u.responseTokens !== undefined)
    rows.push({ label: "Output", tokens: u.responseTokens, hint: "output_tokens — tokens generated in the response" });
  return rows;
});
const totalTokens = computed(() => usageRows.value.reduce((sum, r) => sum + r.tokens, 0));
const errorMsg = computed(() => typeof attrs.value.error === "string" ? attrs.value.error : null);

const verbBadgeClass = computed(() => {
  switch (verb.value) {
    case "decide":  return "verb--decide";
    case "extract": return "verb--extract";
    case "ask":     return "verb--ask";
    case "task":    return "verb--task";
    case "converse": return "verb--converse";
    default:        return "verb--other";
  }
});

// ── Agent actions drawer ─────────────────────────────────────────────────────
// The transcript_ref pointer on agent.call.complete (and agent.call.error)
// gates the affordance. transcript_ref.events is the badge count; the actual
// events are fetched lazily on first open via the DataSource (LiveSource hits
// the RPC, SnapshotSource resolves the inlined attrs.transcript).
interface TranscriptRef {
  format?: string;
  path?: string;
  events?: number;
  schema_version?: number;
}
const transcriptRef = computed<TranscriptRef | null>(() => {
  const r = attrs.value.transcript_ref;
  return r && typeof r === "object" ? (r as TranscriptRef) : null;
});
const transcriptEvents = computed(() => transcriptRef.value?.events ?? 0);
const callId = computed(() =>
  typeof attrs.value.call_id === "string" ? attrs.value.call_id : ""
);
const resolvedSessionId = computed(
  () => props.sessionId || props.event.session_id || ""
);

const drawerOpen = ref(false);
const loading = ref(false);
const loadError = ref("");
const transcript = ref<TranscriptData | null>(null);

async function toggleDrawer(): Promise<void> {
  drawerOpen.value = !drawerOpen.value;
  if (drawerOpen.value && !transcript.value && !loading.value) {
    await loadTranscript();
  }
}

async function loadTranscript(): Promise<void> {
  if (!callId.value) {
    loadError.value = "No call_id on this event.";
    return;
  }
  loading.value = true;
  loadError.value = "";
  try {
    transcript.value = await createDataSource().getTranscript(
      resolvedSessionId.value,
      callId.value
    );
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

// ── Cassette-vs-live drift re-run ────────────────────────────────────────────
// Only meaningful live (a snapshot has no server to re-run against). Re-running
// fetches a FRESH live transcript for the same call_id; TranscriptDiff then
// diffs the recorded `data` against it. Under deterministic replay the paths are
// identical — the honest "no drift" state — but a genuine drift shows up here.
const liveTranscript = ref<TranscriptData | null>(null);
const rerunning = ref(false);
const canRerun = computed(() => {
  const win = globalThis as typeof globalThis & { __KITSOKI_SNAPSHOT__?: unknown };
  return win.__KITSOKI_SNAPSHOT__ === undefined && !!resolvedSessionId.value;
});

async function rerunLive(): Promise<void> {
  if (!canRerun.value || !callId.value) return;
  rerunning.value = true;
  try {
    // The live re-run reads the current sidecar for this call_id afresh. (A full
    // re-execution is a separate, opt-in capability; here we surface the diff
    // seam over whatever live sidecar exists, which under pure replay equals the
    // recorded one — TranscriptDiff renders that as "no drift".)
    liveTranscript.value = await new LiveSource("/").getTranscript(
      resolvedSessionId.value,
      callId.value
    );
  } catch {
    // Swallow: the diff control simply stays in its no-compare state.
  } finally {
    rerunning.value = false;
  }
}
</script>

<style scoped>
.agent-detail {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.agent-detail__header {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  flex-wrap: wrap;
  padding-bottom: 0.35rem;
  border-bottom: 1px solid var(--k-border, #1e293b);
}

.agent-detail__verb-badge {
  padding: 0.1rem 0.5rem;
  border-radius: 3px;
  font-size: 0.75rem;
  font-weight: 700;
  font-family: ui-monospace, monospace;
  text-transform: uppercase;
}

/* Verb badge colours */
.verb--decide  { background: #1e1b4b; color: #a5b4fc; border: 1px solid #3730a3; }
.verb--extract { background: #042f2e; color: #5eead4; border: 1px solid #0d9488; }
.verb--ask     { background: #431407; color: #fdba74; border: 1px solid #c2410c; }
.verb--task    { background: #450a0a; color: #fca5a5; border: 1px solid #991b1b; }
.verb--converse { background: #083344; color: #67e8f9; border: 1px solid #0891b2; }
.verb--other   { background: var(--k-bg-input, #1e293b); color: var(--k-fg-muted, #94a3b8); border: 1px solid #334155; }

.agent-detail__usage {
  border-collapse: collapse;
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  align-self: flex-start;
  min-width: 16rem;
  background: var(--k-bg-inset, #080f1a);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 4px;
  overflow: hidden;
}

.agent-detail__usage-th {
  text-align: left;
  color: var(--k-fg-muted, #64748b);
  font-weight: 600;
  text-transform: uppercase;
  font-size: 0.65rem;
  letter-spacing: 0.03em;
  padding: 0.3rem 0.6rem;
  border-bottom: 1px solid var(--k-border, #1e293b);
}

.agent-detail__usage-th--num {
  text-align: right;
}

.agent-detail__usage-label {
  color: var(--k-fg-muted, #94a3b8);
  padding: 0.18rem 0.6rem;
}

.agent-detail__usage-num {
  color: var(--k-fg, #cbd5e1);
  text-align: right;
  padding: 0.18rem 0.6rem;
  font-variant-numeric: tabular-nums;
}

.agent-detail__usage-total td {
  border-top: 1px solid var(--k-border, #1e293b);
  color: var(--k-fg, #e2e8f0);
  font-weight: 600;
  padding-top: 0.28rem;
}

.agent-detail__usage-cost td {
  color: #a3e635;
  font-weight: 600;
}

.agent-detail__meta {
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.75rem;
  font-family: ui-monospace, monospace;
}

.agent-detail__meta--model {
  color: var(--k-fg-muted, #64748b);
  font-size: 0.7rem;
}

.agent-detail__spacer {
  flex: 1;
}

.agent-detail__stat {
  color: var(--k-fg-muted, #64748b);
  font-size: 0.7rem;
  font-family: ui-monospace, monospace;
  white-space: nowrap;
}

.agent-detail__stat--tokens {
  color: var(--k-fg-subtle, #475569);
}

.agent-detail__stat--cost {
  color: #a3e635;
}

.agent-detail__error {
  background: #2d0707;
  border: 1px solid #991b1b;
  border-radius: 4px;
  color: var(--k-error, #fca5a5);
  padding: 0.3rem 0.5rem;
  font-size: 0.75rem;
  font-family: ui-monospace, monospace;
}

.agent-detail__fallback {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}

.agent-detail__pre {
  background: var(--k-bg-inset, #080f1a);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 4px;
  padding: 0.4rem 0.6rem;
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  color: var(--k-fg-code, #7dd3fc);
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
}

/* Agent actions affordance + drawer */
.agent-detail__agent {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  border-top: 1px solid var(--k-border, #1e293b);
  padding-top: 0.4rem;
}

.agent-detail__agent-affordance {
  align-self: flex-start;
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  background: var(--k-bg-inset, #0a1728);
  border: 1px solid #334155;
  color: var(--k-fg-accent, #93c5fd);
  cursor: pointer;
  font-size: 0.75rem;
  font-weight: 600;
  padding: 0.25rem 0.6rem;
  border-radius: 4px;
}

.agent-detail__agent-affordance:hover {
  background: #0f1e38;
  border-color: var(--k-border-focus, #3b82f6);
}

.agent-detail__agent-icon {
  font-size: 0.65rem;
  color: var(--k-fg-muted, #64748b);
}

.agent-detail__agent-drawer {
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 4px;
  padding: 0.5rem;
  background: var(--k-bg-inset, #060b14);
}

.agent-detail__agent-status {
  color: var(--k-fg-muted, #64748b);
  font-size: 0.78rem;
  padding: 0.3rem 0;
}

.agent-detail__agent-status--err {
  color: var(--k-error, #fca5a5);
}
</style>
