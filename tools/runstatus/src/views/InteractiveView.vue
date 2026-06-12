<template>
  <div class="iv">
    <div v-if="store.loading" class="iv__loading">Loading session…</div>
    <template v-else>
      <!-- Top bar -->
      <header class="iv__topbar">
        <router-link to="/" class="iv__back" data-testid="back-stories">← Stories</router-link>
        <span class="iv__app-id">{{ appId }}</span>
        <span class="iv__sep">·</span>
        <code class="iv__current-state" data-testid="current-state">{{ store.currentStatePath || "—" }}</code>
        <span
          class="iv__state-badge"
          data-testid="state-badge"
          :data-terminal="store.terminal ? 'true' : 'false'"
          :class="store.terminal ? 'iv__state-badge--done' : 'iv__state-badge--live'"
        >
          {{ store.terminal ? 'done' : 'live' }}
        </span>
        <span
          v-if="store.usageTotals.present"
          class="iv__usage"
          :title="`${store.usageTotals.calls} oracle calls · in ${fmtTokens(store.usageTotals.promptTokens)} / out ${fmtTokens(store.usageTotals.responseTokens)} tokens`"
        >
          Σ {{ fmtTokens(store.usageTotals.promptTokens + store.usageTotals.responseTokens) }} tok<template v-if="fmtCost(store.usageTotals.costUsd)"> · {{ fmtCost(store.usageTotals.costUsd) }}</template>
        </span>
        <StoryFreshness
          :session-id="sessionId"
          :on-reloaded="onFreshnessReloaded"
          :on-reload-error="onFreshnessError"
          data-testid="story-freshness-widget"
        />
        <router-link :to="`/s/${sessionId}`" class="iv__observe-link" data-testid="observe-link">Observe ↗</router-link>
      </header>

      <!-- Reload warning: shown when the current state was removed by the edit. -->
      <div
        v-if="reloadWarning"
        class="iv__reload-warning"
        data-testid="reload-warning"
      >
        {{ reloadWarning }}
      </div>

      <!-- Main row: chat (left) | trace (right) -->
      <div class="iv__main">
        <!-- LEFT: conversation -->
        <section class="iv__chat" aria-label="Conversation" data-testid="chat-section">
          <ChatTranscript class="iv__transcript" :transcript="store.transcript" />
          <!-- Streaming thinking bubble: visible while a turn is in flight -->
          <div v-if="pending" class="iv__thinking" data-testid="thinking-bubble">
            <div class="iv__thinking-avatar">A</div>
            <div class="iv__thinking-bubble">
              <div class="iv__thinking-role">Agent</div>
              <!-- The live feed, in arrival order: thinking prose (🧠, like the
                   TUI) interleaved with the tool calls it explains — the same
                   shared ActivityFeed the preserved disclosure and the meta
                   overlay render, so the stream looks identical everywhere. -->
              <ActivityFeed :items="store.pendingStream" />
              <!-- Trailing dots while the turn is in flight: the bubble only
                   exists mid-turn, so "more is coming" is always true here. -->
              <div class="iv__thinking-dots"><span>·</span><span>·</span><span>·</span></div>
            </div>
          </div>
          <div v-if="store.terminal" class="iv__done-note">
            Session complete — no further input accepted.
          </div>
          <InputBar
            v-else
            :intents="store.currentView?.intents ?? []"
            :typed-view="store.currentView?.typed_view"
            :default-intent="store.currentView?.default_intent"
            :pending="pending"
            @send="onSend"
            @intent="onIntent"
          />
          <div v-if="error" class="iv__error">{{ error }}</div>
        </section>

        <!-- RIGHT: live trace (diagram over timeline) -->
        <section class="iv__trace" aria-label="Trace">
          <div class="iv__panel iv__panel--diagram" data-testid="trace-diagram">
            <div class="iv__panel-header">State Diagram</div>
            <StateDiagram
              v-if="store.mermaid"
              :mermaid-source="store.mermaid.source"
              :node-map="store.mermaid.node_map"
              :current-state-path="store.currentStatePath"
              :highlighted-state-paths="store.highlightedStatePaths"
              :events="store.events"
              :selected-event-index="store.selectedEventIndex"
              :intents="store.currentView?.intents ?? []"
              @select="onNodeSelect"
              @select-phase="onPhaseSelect"
              @select-event="onEventSelect"
            />
            <div v-else class="iv__empty">No diagram.</div>
          </div>
          <div class="iv__panel iv__panel--timeline" data-testid="trace-timeline">
            <div class="iv__panel-header">
              <span>Trace</span>
              <button
                v-if="store.highlightedStatePaths.length > 0"
                class="iv__clear-highlight"
                title="Clear diagram highlight"
                @click="onClearHighlight"
              >clear highlight ({{ store.highlightedStatePaths.length }})</button>
            </div>
            <TraceTimeline
              :events="store.events"
              :selected-event-index="store.selectedEventIndex"
              :highlighted-state-paths="store.highlightedStatePaths"
              :highlight-tick="store.highlightTick"
              :mermaid-source="store.mermaid?.source ?? null"
              @select="onEventSelect"
            />
          </div>
        </section>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { useRunStore } from "../stores/run.js";
import { useInboxStore } from "../stores/inbox.js";
import { createDataSource } from "../data/source.js";
import type { DataSource } from "../data/source.js";
import { LiveSource } from "../data/live-source.js";
import { markAutoNavDone } from "../lib/auto-nav.js";
import ActivityFeed from "../components/ActivityFeed.vue";
import ChatTranscript from "../components/ChatTranscript.vue";
import InputBar from "../components/InputBar.vue";
import StateDiagram from "../components/StateDiagram.vue";
import TraceTimeline from "../components/TraceTimeline.vue";
import StoryFreshness from "../components/StoryFreshness.vue";
import { fmtTokens, fmtCost } from "../components/oracle/lib.js";
import type { NodeRef } from "../types.js";

const props = defineProps<{ sessionId: string }>();
const store = useRunStore();
const route = useRoute();
const router = useRouter();
const inbox = useInboxStore();

// One DataSource for the lifetime of the view (subscribe + write RPCs).
let source: DataSource | null = null;

// True while a turn is in flight; disables the input so the operator can't
// fire a second overlapping turn against the live session.
const pending = ref(false);
const error = ref<string | null>(null);

const appId = computed(() => store.appDef?.id ?? store.appDef?.name ?? "kitsoki");

const reloadWarning = ref<string | null>(null);

function onFreshnessReloaded(prevStateExists: boolean): void {
  reloadWarning.value = prevStateExists ? null : "current state removed; staying put";
}

function onFreshnessError(msg: string): void {
  reloadWarning.value = msg;
}

async function loadSession(sessionId: string): Promise<void> {
  if (!source) source = createDataSource();
  // hydrate resets prior session state, loads session/app/mermaid/trace, and
  // opens the live subscription; loadInitialView seeds currentView + the
  // opening agent transcript entry.
  await store.hydrate(source, sessionId);
  await store.loadInitialView(source, sessionId);
  await maybeTeleportFromQuery(sessionId);
}

/**
 * Inbox deep-link: if the route carries `?notif=<id>`, teleport the session to
 * that notification's target room, apply the resulting view, mark the
 * notification read, then clear the query param via router.replace so a refresh
 * doesn't re-teleport. A non-teleportable / unknown id rejects with -32000 — we
 * surface it as a soft error and still clear the param. Runs AFTER hydrate so
 * the run store is ready to receive the TurnResult.
 */
async function maybeTeleportFromQuery(sessionId: string): Promise<void> {
  // Guard for mounts without a router (some unit tests mount the view bare).
  if (!route || !router) return;
  const raw = route.query.notif;
  const notifId = Array.isArray(raw) ? raw[0] : raw;
  if (!notifId || typeof notifId !== "string") return;
  const live = new LiveSource("/");
  try {
    const result = await live.teleport(sessionId, notifId);
    store.applyTurnResult(result);
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  }
  void inbox.markRead(live, sessionId, notifId);
  // Clear the param so a page refresh doesn't re-fire the teleport.
  const q = { ...route.query };
  delete q.notif;
  await router.replace({ path: route.path, query: q });
}

onMounted(() => {
  // Viewing a session spends the per-tab auto-nav convenience: if this view is
  // the tab's first mount (a pasted/bookmarked /s/:id/chat link, or the push
  // right after starting a session), the home screen must NOT later bounce the
  // user back in when they click "← Stories" with one live session.
  markAutoNavDone();
  void loadSession(props.sessionId);

  // Demo / tour test hook: submit an explicit intent through THIS view's own
  // store path (the same code path InputBar's @intent uses), so the chat +
  // InputBar re-render reactively — unlike an out-of-band session.submit RPC,
  // which advances the engine but leaves this view stale. Mirrors the
  // window.__startTourWithSteps hook the tour video specs rely on. Used to
  // drive semantic-routing rooms (no intent buttons) on-camera deterministically
  // in the no-LLM --flow posture. Inert unless a spec calls it.
  (window as unknown as {
    __kitsokiSubmitIntent?: (name: string, slots?: Record<string, unknown>) => Promise<void>;
  }).__kitsokiSubmitIntent = async (name: string, slots: Record<string, unknown> = {}) => {
    if (!source) return;
    await runTurn(() => store.submitIntent(source!, props.sessionId, name, slots));
  };
});

// Switching directly between two /s/:sessionId/chat routes reuses this
// component (only the param changes), so onMounted never re-fires. Re-load on
// sessionId change so the new session's chat isn't left showing the old one.
watch(
  () => props.sessionId,
  (next) => {
    void loadSession(next);
  }
);

onUnmounted(() => {
  store.teardown();
  delete (window as unknown as { __kitsokiSubmitIntent?: unknown }).__kitsokiSubmitIntent;
});

/**
 * Run a write action with the pending guard. The store actions push the
 * user/agent transcript entries and apply the result; we only manage the
 * in-flight flag and surface transport-level errors here. Guard rejections /
 * clarifications ride back inside the TurnResult and are rendered as agent
 * transcript entries, so they are NOT errors.
 */
async function runTurn(fn: () => Promise<unknown>): Promise<void> {
  if (pending.value || !source || store.terminal) return;
  pending.value = true;
  error.value = null;
  try {
    await fn();
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    pending.value = false;
  }
}

/**
 * Raw-text submission from semantic routing rooms (the free-text textarea).
 * Routes via session.turn so the semantic router handles natural-language
 * dispatch. Text-slot intent forms use onIntent / session.submit instead.
 */
function onSend(text: string, _intentName: string): void {
  if (!source) return;
  void runTurn(() => store.sendText(source!, props.sessionId, text));
}

function onIntent(name: string, slots: Record<string, unknown>, displayLabel?: string): void {
  if (!source) return;
  void runTurn(() => store.submitIntent(source!, props.sessionId, name, slots, displayLabel));
}

// ---- trace interactions (mirror RunView observer behavior) ----
function onNodeSelect(_nodeId: string, nodeRef: NodeRef): void {
  if (nodeRef.kind === "state") {
    store.setHighlightedStatePaths([nodeRef.ref]);
  }
}
function onPhaseSelect(_phaseId: string, roomRefs: string[]): void {
  store.setHighlightedStatePaths(roomRefs);
}
function onClearHighlight(): void {
  store.setHighlightedStatePaths([]);
}
function onEventSelect(index: number): void {
  store.selectEvent(index);
}
</script>

<style scoped>
.iv {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background: #0a1120;
  color: #e2e8f0;
  overflow: hidden;
}

.iv__loading {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: #64748b;
  font-size: 1rem;
}

/* ---- Top bar ---- */
.iv__topbar {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.55rem 1rem;
  background: #0f172a;
  border-bottom: 1px solid #1e293b;
  flex-shrink: 0;
  font-size: 0.8125rem;
}

.iv__back {
  color: #60a5fa;
  text-decoration: none;
}
.iv__back:hover {
  text-decoration: underline;
}

.iv__app-id {
  font-weight: 600;
  color: #e2e8f0;
}

.iv__sep {
  color: #334155;
}

.iv__current-state {
  font-family: ui-monospace, monospace;
  font-size: 0.775rem;
  color: #7dd3fc;
}

.iv__state-badge {
  display: inline-block;
  padding: 0.1rem 0.45rem;
  border-radius: 999px;
  font-size: 0.7rem;
  font-weight: 600;
}
.iv__state-badge--live {
  background: #14532d;
  color: #86efac;
}
.iv__state-badge--done {
  background: #1e293b;
  color: #64748b;
}

.iv__usage {
  font-family: ui-monospace, monospace;
  font-size: 0.75rem;
  color: #a3e635;
  background: #1a2e05;
  border: 1px solid #3f6212;
  border-radius: 4px;
  padding: 0.1rem 0.45rem;
  white-space: nowrap;
}

.iv__observe-link {
  margin-left: auto;
  color: #94a3b8;
  text-decoration: none;
  font-size: 0.75rem;
}
.iv__observe-link:hover {
  color: #cbd5e1;
  text-decoration: underline;
}

/* ---- Main row ---- */
.iv__reload-warning {
  flex-shrink: 0;
  padding: 0.25rem 1rem;
  font-size: 0.8rem;
  background: #1c1107;
  border-bottom: 1px solid #92400e;
  color: #fcd34d;
}

.iv__main {
  display: flex;
  flex: 1;
  min-height: 0;
  gap: 0;
}

/* LEFT: chat column */
.iv__chat {
  display: flex;
  flex-direction: column;
  flex: 1 1 46%;
  min-width: 0;
  min-height: 0;
  border-right: 1px solid #1e293b;
  background: #0f1115;
}

.iv__transcript {
  flex: 1 1 auto;
  min-height: 0;
}

.iv__done-note {
  padding: 0.6rem 1.1rem;
  font-size: 0.8rem;
  color: #64748b;
  background: #14171d;
  border-top: 1px solid #2a2f3a;
  text-align: center;
}

.iv__error {
  padding: 0.5rem 1.1rem;
  font-size: 0.78rem;
  color: #fca5a5;
  background: #2a1518;
  border-top: 1px solid #7f1d1d;
}

/* RIGHT: trace column */
.iv__trace {
  display: flex;
  flex-direction: column;
  flex: 1 1 54%;
  min-width: 0;
  min-height: 0;
  padding: 0.5rem;
  gap: 0.5rem;
}

.iv__panel {
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border-radius: 6px;
  min-height: 0;
}

.iv__panel--diagram {
  flex: 1 1 45%;
}

.iv__panel--timeline {
  flex: 1 1 55%;
}

.iv__panel-header {
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: #64748b;
  padding: 0.25rem 0;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.iv__clear-highlight {
  background: #3a2d0e;
  border: 1px solid #fbbf24;
  color: #fde68a;
  font-size: 0.65rem;
  text-transform: none;
  letter-spacing: normal;
  padding: 0.1rem 0.4rem;
  border-radius: 999px;
  cursor: pointer;
  font-family: inherit;
}
.iv__clear-highlight:hover {
  background: #4a3a14;
}

.iv__panel--diagram :deep(.state-diagram) {
  flex: 1;
  height: 100%;
}

.iv__panel--timeline :deep(.trace-timeline) {
  flex: 1;
  height: 100%;
  min-height: 0;
}

.iv__empty {
  color: #475569;
  font-size: 0.875rem;
  padding: 1rem;
}

/* ---- Streaming thinking bubble ---- */
.iv__thinking {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 8px 24px 0;
  max-width: 98%;
}

.iv__thinking-avatar {
  flex: 0 0 auto;
  width: 32px;
  height: 32px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 13px;
  font-weight: 600;
  color: #fff;
  background: #475569;
  user-select: none;
}

.iv__thinking-bubble {
  background: #f7f8fa;
  color: #1f2430;
  border: 1px solid #d8dbe2;
  border-radius: 12px;
  border-bottom-left-radius: 4px;
  padding: 10px 14px;
  font-size: 14px;
  line-height: 1.5;
  min-width: 120px;
  max-width: 100%;
  overflow-wrap: anywhere;
}

.iv__thinking-role {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  opacity: 0.6;
  margin-bottom: 4px;
}

/* The live feed rows (🧠 thoughts + tool calls) come from the shared
   ActivityFeed.vue — the same component the preserved disclosure and the
   meta overlay render, so the stream looks identical everywhere. */

.iv__thinking-dots {
  display: flex;
  gap: 4px;
  font-size: 20px;
  color: #94a3b8;
}

@keyframes iv-dot-pulse {
  0%, 80%, 100% { opacity: 0.2; }
  40% { opacity: 1; }
}

.iv__thinking-dots span:nth-child(1) { animation: iv-dot-pulse 1.4s infinite 0s; }
.iv__thinking-dots span:nth-child(2) { animation: iv-dot-pulse 1.4s infinite 0.2s; }
.iv__thinking-dots span:nth-child(3) { animation: iv-dot-pulse 1.4s infinite 0.4s; }
</style>
