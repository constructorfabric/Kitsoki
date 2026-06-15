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
        <!-- Running oracle spend for this session. Always shown (not gated on
             `present`) so the operator can see the live cost ticking — which in a
             deterministic run is exactly the point: every state transition, guard,
             and git host call is free, so this reads $0.0000 until (and unless) an
             oracle touchpoint fires. The :class flags the zero state for emphasis. -->
        <span
          class="iv__usage"
          :class="{ 'iv__usage--zero': !store.usageTotals.present }"
          data-testid="usage-meter"
          :title="`${store.usageTotals.calls} oracle call(s) · in ${fmtTokens(store.usageTotals.promptTokens)} / out ${fmtTokens(store.usageTotals.responseTokens)} tokens — deterministic transitions and git calls are free`"
        >
          Σ {{ fmtTokens(store.usageTotals.promptTokens + store.usageTotals.responseTokens) }} tok · {{ fmtCost(store.usageTotals.costUsd) }}
        </span>
        <span
          v-if="store.harnessProfiles.length"
          class="iv__harness"
          data-testid="harness-picker"
        >
          <select
            class="iv__harness-select"
            data-testid="provider-select"
            title="Harness profile (backend/provider) — takes effect next turn"
            :value="store.harnessActiveProfile"
            @change="onProviderChange"
          >
            <option v-for="p in store.harnessProfiles" :key="p.name" :value="p.name">{{ p.name }}</option>
          </select>
          <select
            v-if="activeModels.length"
            class="iv__harness-select"
            data-testid="model-select"
            title="Model for the active profile — takes effect next turn"
            :value="activeModel"
            @change="onModelChange"
          >
            <option v-for="m in activeModels" :key="m" :value="m">{{ shortModel(m) }}</option>
          </select>
          <select
            v-if="activeEfforts.length"
            class="iv__harness-select"
            data-testid="effort-select"
            title="Reasoning effort — where the model supports it; takes effect next turn"
            :value="activeEffort"
            @change="onEffortChange"
          >
            <option v-for="e in activeEfforts" :key="e" :value="e">effort: {{ e }}</option>
          </select>
        </span>
        <ProposalsBadge />
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

      <!-- Main row: chat (left) | trace (right).
           Browser: chat 46% | diagram+timeline 54%.
           Embed (VS Code): chat front/center | a thin hint rail that maximizes. -->
      <div class="iv__main" :class="{ 'iv__main--embed': embed, 'iv__main--expanded': embed && !!expanded }">
        <!-- LEFT: conversation -->
        <section class="iv__chat" aria-label="Conversation" data-testid="chat-section">
          <ChatTranscript class="iv__transcript" :transcript="store.chatEntries" />
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

        <!-- RIGHT (browser): live trace (diagram over timeline) -->
        <section v-if="!embed" class="iv__trace" aria-label="Trace">
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

        <!-- RIGHT (embed): hint rail — Trace + Graph as live, maximizable cards.
             Chat stays front/center; a card maximizes beside it (horizontal). -->
        <aside v-else class="iv__side" data-testid="hint-rail">
          <!-- Collapsed: two compact live hints. -->
          <template v-if="!expanded">
            <button
              type="button"
              class="iv__hint"
              data-testid="hint-trace"
              title="Maximize the trace timeline"
              @click="expand('trace')"
            >
              <div class="iv__hint-head">
                <span class="iv__hint-title">Trace</span>
                <span class="iv__hint-grow" aria-hidden="true" data-testid="hint-trace-maximize">⤢</span>
              </div>
              <div class="iv__hint-metric">{{ store.events.length }} <span>events</span></div>
              <div class="iv__hint-row">
                <span class="iv__hint-dot" :class="store.terminal ? 'is-done' : 'is-live'"></span>
                room <code>{{ store.currentStatePath || "—" }}</code>
              </div>
            </button>

            <button
              type="button"
              class="iv__hint"
              data-testid="hint-graph"
              title="Maximize the state diagram"
              @click="expand('graph')"
            >
              <div class="iv__hint-head">
                <span class="iv__hint-title">Graph</span>
                <span class="iv__hint-grow" aria-hidden="true" data-testid="hint-graph-maximize">⤢</span>
              </div>
              <div class="iv__hint-metric">{{ roomCount }} <span>rooms</span></div>
              <div class="iv__hint-row">
                current <code>{{ store.currentStatePath || "—" }}</code>
              </div>
              <div class="iv__hint-row">{{ intentCount }} next intent{{ intentCount === 1 ? '' : 's' }}</div>
            </button>
          </template>

          <!-- Maximized: the full component beside the chat, with a minimize. -->
          <div v-else class="iv__expanded" data-testid="hint-expanded">
            <header class="iv__expanded-head">
              <span class="iv__expanded-title">{{ expanded === 'graph' ? 'State Diagram' : 'Trace' }}</span>
              <button
                type="button"
                class="iv__expanded-switch"
                :data-testid="expanded === 'graph' ? 'switch-trace' : 'switch-graph'"
                @click="expand(expanded === 'graph' ? 'trace' : 'graph')"
              >{{ expanded === 'graph' ? 'Trace ⤢' : 'Graph ⤢' }}</button>
              <button
                type="button"
                class="iv__expanded-min"
                data-testid="expanded-minimize"
                title="Minimize"
                @click="minimize"
              >Minimize ✕</button>
            </header>

            <div v-if="expanded === 'graph'" class="iv__panel iv__panel--diagram" data-testid="trace-diagram">
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

            <div v-else class="iv__panel iv__panel--timeline" data-testid="trace-timeline">
              <TraceTimeline
                :events="store.events"
                :selected-event-index="store.selectedEventIndex"
                :highlighted-state-paths="store.highlightedStatePaths"
                :highlight-tick="store.highlightTick"
                :mermaid-source="store.mermaid?.source ?? null"
                @select="onEventSelect"
              />
            </div>
          </div>
        </aside>
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
import ProposalsBadge from "../components/ProposalsBadge.vue";
import { useProposalsStore } from "../stores/proposals.js";
import type { Proposal } from "../stores/proposals.js";
import { fmtTokens, fmtCost } from "../components/oracle/lib.js";
import { isEmbedded } from "../lib/embed.js";
import type { NodeRef } from "../types.js";

const props = defineProps<{ sessionId: string }>();
const store = useRunStore();
const route = useRoute();
const router = useRouter();
const inbox = useInboxStore();
const proposals = useProposalsStore();

// Embed layout (VS Code webview): chat front/center with a hint rail that
// maximizes trace/graph. The standalone browser app keeps its full layout.
const embed = computed(() => isEmbedded() || route?.query?.embed === "1");

// Which hint, if any, is maximized beside the chat.
const expanded = ref<null | "trace" | "graph">(null);
function expand(which: "trace" | "graph"): void {
  expanded.value = which;
}
function minimize(): void {
  expanded.value = null;
}

// Live hint metrics (cheap — read straight off the store).
const roomCount = computed(
  () =>
    Object.values(store.mermaid?.node_map ?? {}).filter(
      (n) => (n as NodeRef).kind === "state",
    ).length,
);
const intentCount = computed(() => store.currentView?.intents?.length ?? 0);

// One DataSource for the lifetime of the view (subscribe + write RPCs).
let source: DataSource | null = null;

// True while a turn is in flight; disables the input so the operator can't
// fire a second overlapping turn against the live session.
const pending = ref(false);
const error = ref<string | null>(null);

const appId = computed(() => store.appDef?.id ?? store.appDef?.name ?? "kitsoki");

// ── Harness picker (mirrors RunView) ─────────────────────────────────────────
const activeProfileObj = computed(() => store.harnessProfiles.find((p) => p.active));
const activeModels = computed<string[]>(() => activeProfileObj.value?.models ?? []);
const activeModel = computed<string>(() => store.harnessModel || activeProfileObj.value?.model || "");
const activeEfforts = computed<string[]>(() => activeProfileObj.value?.efforts ?? []);
const activeEffort = computed<string>(() => store.harnessEffort || activeProfileObj.value?.effort || "");

async function onProviderChange(e: Event): Promise<void> {
  if (!source) source = createDataSource();
  await store.selectProfile(source, props.sessionId, (e.target as HTMLSelectElement).value);
}
async function onModelChange(e: Event): Promise<void> {
  if (!source) source = createDataSource();
  await store.selectProfile(source, props.sessionId, store.harnessActiveProfile, (e.target as HTMLSelectElement).value, store.harnessEffort);
}
async function onEffortChange(e: Event): Promise<void> {
  if (!source) source = createDataSource();
  await store.selectProfile(source, props.sessionId, store.harnessActiveProfile, store.harnessModel, (e.target as HTMLSelectElement).value);
}
function shortModel(m: string): string {
  const slash = m.lastIndexOf("/");
  return slash >= 0 ? m.slice(slash + 1) : m;
}

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
  // `displayLabel` lets a demo render the operator's REAL verbatim utterance as
  // the user transcript bubble (e.g. the mined "rebase onto main and resolve the
  // conflicts") while the engine deterministically processes the resolved intent
  // — exactly the shape a live LLM session leaves behind (utterance + resolved
  // intent), so the no-LLM video shows real input, not a synthetic intent name.
  (window as unknown as {
    __kitsokiSubmitIntent?: (
      name: string,
      slots?: Record<string, unknown>,
      displayLabel?: string,
    ) => Promise<void>;
  }).__kitsokiSubmitIntent = async (
    name: string,
    slots: Record<string, unknown> = {},
    displayLabel?: string,
  ) => {
    if (!source) return;
    await runTurn(() => store.submitIntent(source!, props.sessionId, name, slots, displayLabel));
  };

  // __kitsokiSendText drives a FREE-TEXT turn through the store's sendText
  // (session.turn → the real routing tiers), so a demo types the operator's
  // verbatim utterance and the engine ROUTES it (semantic tier, no LLM) rather
  // than receiving a pre-resolved intent. This is what makes the routing chip
  // light up on-camera: the turn carries genuine routed_by/match_type
  // provenance. Mirrors __kitsokiSubmitIntent; inert unless a spec calls it.
  (window as unknown as {
    __kitsokiSendText?: (text: string) => Promise<void>;
  }).__kitsokiSendText = async (text: string) => {
    if (!source) return;
    await runTurn(() => store.sendText(source!, props.sessionId, text));
  };

  // Bind the proposals store to the live source so an accepted proposal resolves
  // over answer_question, and expose the deterministic seed seam (mirrors
  // __pushOperatorQuestion) so a no-LLM spec can populate the proposals badge
  // without a real miner. Inert unless a spec calls it.
  if (source instanceof LiveSource) proposals.init(source);
  (window as unknown as {
    __pushProposal?: (proposalJson: string) => void;
  }).__pushProposal = (proposalJson: string) => {
    try {
      proposals.push(JSON.parse(proposalJson) as Proposal);
    } catch {
      /* malformed JSON — ignore (deterministic test/demo driver only) */
    }
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
  proposals.teardown();
  delete (window as unknown as { __kitsokiSubmitIntent?: unknown }).__kitsokiSubmitIntent;
  delete (window as unknown as { __kitsokiSendText?: unknown }).__kitsokiSendText;
  delete (window as unknown as { __pushProposal?: unknown }).__pushProposal;
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
  background: var(--k-bg-deep, #0a1120);
  color: var(--k-fg, #e2e8f0);
  overflow: hidden;
}

.iv__loading {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  color: var(--k-fg-muted, #64748b);
  font-size: 1rem;
}

/* ---- Top bar ---- */
.iv__topbar {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.55rem 1rem;
  background: var(--k-bg-widget, #0f172a);
  border-bottom: 1px solid var(--k-border, #1e293b);
  flex-shrink: 0;
  font-size: 0.8125rem;
}

.iv__back {
  color: var(--k-fg-accent, #60a5fa);
  text-decoration: none;
}
.iv__back:hover {
  text-decoration: underline;
}

.iv__app-id {
  font-weight: 600;
  color: var(--k-fg, #e2e8f0);
}

.iv__sep {
  color: var(--k-fg-subtle, #334155);
}

.iv__current-state {
  font-family: ui-monospace, monospace;
  font-size: 0.775rem;
  color: var(--k-fg-code, #7dd3fc);
}

.iv__state-badge {
  display: inline-block;
  padding: 0.1rem 0.45rem;
  border-radius: 999px;
  font-size: 0.7rem;
  font-weight: 600;
}
.iv__state-badge--live {
  background: var(--k-success-bg, #14532d);
  color: var(--k-success, #86efac);
}
.iv__state-badge--done {
  background: var(--k-bg-input, #1e293b);
  color: var(--k-fg-muted, #64748b);
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
/* No spend yet — the deterministic-run steady state. Brighter green so the
   "this is free" signal reads at a glance while the operator drives. */
.iv__usage--zero {
  color: #bef264;
  border-color: #4d7c0f;
}

.iv__harness {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.iv__harness-select {
  background: #111c33;
  color: #cbd5e1;
  border: 1px solid #2b3a55;
  border-radius: 4px;
  font-size: 12px;
  padding: 2px 4px;
  max-width: 200px;
}
.iv__harness-select:hover {
  border-color: #3b82f6;
}
.iv__observe-link {
  margin-left: auto;
  color: var(--k-fg-muted, #94a3b8);
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
  color: var(--k-warning, #fcd34d);
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
  border-right: 1px solid var(--k-border, #1e293b);
  background: var(--k-bg-inset, #0f1115);
}

.iv__transcript {
  flex: 1 1 auto;
  min-height: 0;
}

.iv__done-note {
  padding: 0.6rem 1.1rem;
  font-size: 0.8rem;
  color: var(--k-fg-muted, #64748b);
  background: var(--k-bg-inset, #14171d);
  border-top: 1px solid var(--k-border-subtle, #2a2f3a);
  text-align: center;
}

.iv__error {
  padding: 0.5rem 1.1rem;
  font-size: 0.78rem;
  color: var(--k-error, #fca5a5);
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
  color: var(--k-fg-muted, #64748b);
  padding: 0.25rem 0;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.iv__clear-highlight {
  background: #3a2d0e;
  border: 1px solid var(--k-warning, #fbbf24);
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
  color: var(--k-fg-subtle, #475569);
  font-size: 0.875rem;
  padding: 1rem;
}

/* ---- Embed layout (VS Code webview): chat front/center + hint rail ---- */
/* Chat is dominant; the right side is a thin rail of live, maximizable cards.
   When a card is maximized the rail widens to ~56% (horizontal split) and the
   chat stays visible — never replaced. */
/* A gutter so the chat content (the InputBar's Send sits at the chat's right
   edge) never abuts the rail, plus z-index so the chat wins the hit-test at the
   seam. */
.iv__main--embed {
  gap: 10px;
}
.iv__main--embed .iv__chat {
  flex: 1 1 auto;
  position: relative;
  z-index: 1;
}
/* Let the intent-form inputs shrink below their intrinsic width so the row (and
   its Send) always fits the chat column — never overflowing under the rail. */
.iv__main--embed .iv__chat :deep(.input-bar__input) {
  min-width: 0;
}

.iv__side {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  flex: 0 0 240px;
  min-width: 0;
  min-height: 0;
  padding: 0.6rem;
  background: var(--k-bg-deep, #0b1220);
  border-left: 1px solid var(--k-border, #1e293b);
  transition: flex-basis 0.18s ease;
}
/* Maximized: a deterministic 44/56 split (both percentage bases compete on equal
   grow), so the panel is prominent while the chat stays front/center. Overrides
   the collapsed-rail `flex: 1 1 auto` chat basis. */
.iv__main--expanded .iv__chat {
  flex: 1 1 44%;
}
.iv__main--expanded .iv__side {
  flex: 1 1 56%;
}

/* Collapsed hint card */
.iv__hint {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  text-align: left;
  font: inherit;
  color: #cbd5e1;
  background: var(--k-bg-widget, #0f172a);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 8px;
  padding: 0.7rem 0.8rem;
  cursor: pointer;
}
.iv__hint:hover {
  border-color: var(--k-border-subtle, #334155);
  background: var(--k-bg-hover, #131f38);
}
.iv__hint-head {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.iv__hint-title {
  font-size: 0.72rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--k-fg-muted, #94a3b8);
}
.iv__hint-grow {
  margin-left: auto;
  color: var(--k-fg-muted, #64748b);
  font-size: 0.95rem;
}
.iv__hint:hover .iv__hint-grow {
  color: var(--k-fg-accent, #93c5fd);
}
.iv__hint-metric {
  font-size: 1.45rem;
  font-weight: 700;
  color: var(--k-fg, #e2e8f0);
  line-height: 1.1;
}
.iv__hint-metric span {
  font-size: 0.7rem;
  font-weight: 500;
  color: var(--k-fg-muted, #64748b);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}
.iv__hint-row {
  display: flex;
  align-items: center;
  gap: 0.35rem;
  font-size: 0.72rem;
  color: var(--k-fg-muted, #94a3b8);
}
.iv__hint-row code {
  font-family: ui-monospace, monospace;
  color: var(--k-fg-code, #7dd3fc);
}
.iv__hint-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}
.iv__hint-dot.is-live {
  background: var(--k-success, #34d399);
  box-shadow: 0 0 0 3px rgba(52, 211, 153, 0.15);
}
.iv__hint-dot.is-done {
  background: var(--k-fg-subtle, #475569);
}

/* Maximized panel */
.iv__expanded {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  min-height: 0;
  min-width: 0;
  gap: 0.4rem;
}
.iv__expanded-head {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-shrink: 0;
}
.iv__expanded-title {
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--k-fg-muted, #64748b);
}
.iv__expanded-switch,
.iv__expanded-min {
  font: inherit;
  font-size: 0.68rem;
  cursor: pointer;
  border-radius: 999px;
  padding: 0.12rem 0.5rem;
  background: var(--k-bg-widget, #0f172a);
  border: 1px solid var(--k-border, #1e293b);
  color: var(--k-fg-muted, #94a3b8);
}
.iv__expanded-switch {
  margin-left: auto;
}
.iv__expanded-switch:hover,
.iv__expanded-min:hover {
  border-color: var(--k-border-subtle, #334155);
  color: #cbd5e1;
}
.iv__expanded .iv__panel {
  flex: 1 1 auto;
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
  background: var(--k-fg-subtle, #475569);
  user-select: none;
}

.iv__thinking-bubble {
  background: var(--k-paper-bg, #f7f8fa);
  color: var(--k-paper-fg, #1f2430);
  border: 1px solid var(--k-paper-border, #d8dbe2);
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
  color: var(--k-fg-muted, #94a3b8);
}

@keyframes iv-dot-pulse {
  0%, 80%, 100% { opacity: 0.2; }
  40% { opacity: 1; }
}

.iv__thinking-dots span:nth-child(1) { animation: iv-dot-pulse 1.4s infinite 0s; }
.iv__thinking-dots span:nth-child(2) { animation: iv-dot-pulse 1.4s infinite 0.2s; }
.iv__thinking-dots span:nth-child(3) { animation: iv-dot-pulse 1.4s infinite 0.4s; }
</style>
