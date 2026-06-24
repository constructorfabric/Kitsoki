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
        <!-- Running agent spend for this session. Always shown (not gated on
             `present`) so the operator can see the live cost ticking — which in a
             deterministic run is exactly the point: every state transition, guard,
             and git host call is free, so this reads $0.0000 until (and unless) an
             agent touchpoint fires. The :class flags the zero state for emphasis. -->
        <span
          class="iv__usage"
          :class="{ 'iv__usage--zero': !store.usageTotals.present }"
          data-testid="usage-meter"
          :title="`${store.usageTotals.calls} agent call(s) · in ${fmtTokens(store.usageTotals.promptTokens)} / out ${fmtTokens(store.usageTotals.responseTokens)} tokens — deterministic transitions and git calls are free`"
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
        <MetaButton v-if="embed" placement="topbar" />
      </header>

      <!-- Reconnecting banner: the live trace stream dropped and the transport
           is backing off + reopening. Without this a stalled stream looks
           identical to a slow agent — dead air. -->
      <div
        v-if="store.connectionState === 'reconnecting'"
        class="iv__reconnecting"
        data-testid="reconnecting-banner"
        role="status"
      >
        <span class="iv__reconnecting-dot" aria-hidden="true"></span>
        Reconnecting to session…
      </div>

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
           Embed (VS Code): chat ONLY — trace + graph live in their own dockable
           windows (the "Kitsoki Surfaces" panels), so the chat panel never repeats
           them. -->
      <div class="iv__main" :class="{ 'iv__main--embed': embed }">
        <!-- LEFT: conversation -->
        <section class="iv__chat" aria-label="Conversation" data-testid="chat-section">
          <div
            v-if="focusedChat || focusedChatLoading || focusedChatError"
            class="iv__focused-chat"
            data-testid="focused-chat"
          >
            <div class="iv__focused-chat-head">
              <span class="iv__focused-chat-label">Subagent</span>
              <strong>{{ focusedChat?.chat.title || focusedChatID || "Loading chat" }}</strong>
              <button
                type="button"
                class="iv__focused-chat-close"
                data-testid="focused-chat-close"
                title="Close focused chat context"
                @click="clearFocusedChat"
              >close</button>
            </div>
            <div v-if="focusedChatLoading" class="iv__focused-chat-muted">Loading focused context...</div>
            <div v-else-if="focusedChatError" class="iv__focused-chat-error">{{ focusedChatError }}</div>
            <template v-else-if="focusedChat">
              <div class="iv__focused-chat-meta">
                <span v-if="focusedChat.context?.session_id">session {{ focusedChat.context.session_id }}</span>
                <span v-if="focusedChatScope">scope {{ focusedChatScope }}</span>
                <span>chat {{ focusedChat.chat.id }}</span>
                <span>{{ focusedChat.chat.status }}</span>
                <span v-if="focusedChat.pty">tmux {{ focusedChat.pty.tmux_session }}</span>
                <span v-if="focusedChat.pty?.mode">{{ focusedChat.pty.mode }}</span>
              </div>
              <div v-if="focusedChat.messages?.length" class="iv__focused-chat-messages">
                <div
                  v-for="m in focusedChatPreview"
                  :key="m.seq"
                  class="iv__focused-chat-message"
                >
                  <span class="iv__focused-chat-role">{{ m.role }}</span>
                  <span class="iv__focused-chat-content">{{ m.content }}</span>
                </div>
              </div>
            </template>
          </div>
          <ChatTranscript
            class="iv__transcript"
            :transcript="store.chatEntries"
            @rewind="onRewind"
          />
          <!-- Streaming thinking bubble: visible while a turn is in flight -->
          <div v-if="pending" class="iv__thinking" data-testid="thinking-bubble">
            <div class="iv__thinking-avatar">A</div>
            <div class="iv__thinking-bubble">
              <div class="iv__thinking-head">
              <div class="iv__thinking-role">Agent</div>
              <!-- Stop: cancels the agent server-side, not just the frontend. -->
              <button
                type="button"
                class="iv__thinking-stop"
                data-testid="cancel-agent"
                :disabled="cancelling"
                @click="onCancel"
              >
                {{ cancelling ? "Stopping…" : "■ Stop" }}
              </button>
            </div>
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

        <!-- Embed (VS Code): nothing beside the chat — Trace and Graph open as
             their own dockable windows via "Kitsoki: Open Trace" / "Open Graph". -->
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
import { LiveSource, TurnCancelledError } from "../data/live-source.js";
import type { ChatMessageItem, ChatShowResult } from "../data/live-source.js";
import { markAutoNavDone } from "../lib/auto-nav.js";
import ActivityFeed from "../components/ActivityFeed.vue";
import ChatTranscript from "../components/ChatTranscript.vue";
import InputBar from "../components/InputBar.vue";
import StateDiagram from "../components/StateDiagram.vue";
import TraceTimeline from "../components/TraceTimeline.vue";
import StoryFreshness from "../components/StoryFreshness.vue";
import MetaButton from "../components/meta/MetaButton.vue";
import ProposalsBadge from "../components/ProposalsBadge.vue";
import { useProposalsStore } from "../stores/proposals.js";
import type { Proposal } from "../stores/proposals.js";
import { fmtTokens, fmtCost } from "../components/agent/lib.js";
import { isEmbedded } from "../lib/embed.js";
import type { NodeRef } from "../types.js";

const GITHUB_INBOX_REFRESH_INTERVAL_MS = 5 * 60 * 1000;

const props = defineProps<{ sessionId: string }>();
const store = useRunStore();
const route = useRoute();
const router = useRouter();
const inbox = useInboxStore();
const proposals = useProposalsStore();

// Embed layout (VS Code webview): chat front/center with a hint rail that
// chat is shown ALONE (trace + graph have their own dockable windows). The
// standalone browser app keeps its full layout (chat | diagram + timeline).
const embed = computed(() => isEmbedded() || route?.query?.embed === "1");

// One DataSource for the lifetime of the view (subscribe + write RPCs).
let source: DataSource | null = null;
let githubInboxTimer: ReturnType<typeof setInterval> | null = null;
let focusedChatSeq = 0;

// True while a turn is in flight; disables the input so the operator can't
// fire a second overlapping turn against the live session.
const pending = ref(false);
// True between clicking Stop and the turn actually aborting — keeps the button
// from firing a second cancel and gives the operator immediate feedback.
const cancelling = ref(false);
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
const focusedChat = ref<ChatShowResult | null>(null);
const focusedChatLoading = ref(false);
const focusedChatError = ref<string | null>(null);
const focusedChatID = ref("");
const focusedChatPreview = computed<ChatMessageItem[]>(() =>
  (focusedChat.value?.messages ?? []).slice(-3)
);
const focusedChatScope = computed(() => {
  const chat = focusedChat.value?.chat;
  if (!chat) return "";
  return chat.display_scope_key || chat.scope_key || "";
});

function canListWork(candidate: DataSource | null): candidate is DataSource & Pick<LiveSource, "listWork"> {
  return typeof (candidate as Partial<Pick<LiveSource, "listWork">> | null)?.listWork === "function";
}

function canSyncGitHubInbox(
  candidate: DataSource | null,
): candidate is DataSource & Pick<LiveSource, "syncGitHubInbox" | "listWork"> {
  const partial = candidate as Partial<Pick<LiveSource, "syncGitHubInbox" | "listWork">> | null;
  return typeof partial?.syncGitHubInbox === "function" && typeof partial.listWork === "function";
}

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
  await maybeSeedProposalsFromQuery();
  await maybeTeleportFromQuery(sessionId);
  await maybeShowChatFromQuery(sessionId);
  startGitHubInboxPolling(sessionId);
}

function startGitHubInboxPolling(sessionId: string): void {
  stopGitHubInboxPolling();
  if (!canSyncGitHubInbox(source)) return;
  void inbox.syncGitHub(source, sessionId, undefined, { silent: true });
  githubInboxTimer = setInterval(() => {
    if (!canSyncGitHubInbox(source)) return;
    void inbox.syncGitHub(source, sessionId, undefined, { silent: true });
  }, GITHUB_INBOX_REFRESH_INTERVAL_MS);
}

function stopGitHubInboxPolling(): void {
  if (!githubInboxTimer) return;
  clearInterval(githubInboxTimer);
  githubInboxTimer = null;
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

async function maybeShowChatFromQuery(sessionId: string): Promise<void> {
  if (!route) return;
  const raw = route.query.chat;
  const chatID = Array.isArray(raw) ? raw[0] : raw;
  if (!chatID || typeof chatID !== "string") {
    focusedChatSeq += 1;
    focusedChat.value = null;
    focusedChatID.value = "";
    focusedChatError.value = null;
    focusedChatLoading.value = false;
    return;
  }
  const seq = ++focusedChatSeq;
  focusedChatID.value = chatID;
  focusedChatLoading.value = true;
  focusedChatError.value = null;
  const live = new LiveSource("/");
  try {
    const result = await live.showChat(sessionId, chatID);
    if (seq !== focusedChatSeq) return;
    focusedChat.value = result;
  } catch (e) {
    if (seq !== focusedChatSeq) return;
    focusedChat.value = null;
    focusedChatError.value = e instanceof Error ? e.message : String(e);
  } finally {
    if (seq === focusedChatSeq) {
      focusedChatLoading.value = false;
    }
  }
}

async function maybeSeedProposalsFromQuery(): Promise<void> {
  if (!route || !router) return;
  const raw = route.query.proposal;
  const encoded = Array.isArray(raw) ? raw : raw ? [raw] : [];
  if (encoded.length === 0) return;

  for (const item of encoded) {
    if (typeof item !== "string") continue;
    try {
      proposals.push(JSON.parse(item) as Proposal);
    } catch {
      /* malformed query seed — ignore (deterministic render/demo path only) */
    }
  }

  const q = { ...route.query };
  delete q.proposal;
  await router.replace({ path: route.path, query: q });
}

async function clearFocusedChat(): Promise<void> {
  focusedChatSeq += 1;
  focusedChat.value = null;
  focusedChatID.value = "";
  focusedChatError.value = null;
  focusedChatLoading.value = false;
  if (!route || !router) return;
  const q = { ...route.query };
  delete q.chat;
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

watch(
  () => route?.query?.chat,
  () => {
    void maybeShowChatFromQuery(props.sessionId);
  }
);

onUnmounted(() => {
  stopGitHubInboxPolling();
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
    // A cancelled turn is a clean operator action, not an error: the server
    // aborted it and persisted nothing, so reset to idle WITHOUT a red toast.
    if (e instanceof TurnCancelledError) {
      // no-op — the pending bubble clears in finally and the room is unchanged
    } else {
      error.value = e instanceof Error ? e.message : String(e);
    }
  } finally {
    pending.value = false;
    cancelling.value = false;
    if (canListWork(source)) {
      await inbox.refreshWork(source);
    }
  }
}

/**
 * Stop the in-flight turn. Fires runstatus.session.cancel, which aborts the
 * agent server-side (not just the frontend); the in-flight turnStream then
 * rejects with TurnCancelledError and runTurn resets to idle. Guarded so a
 * double-click can't fire two cancels.
 */
async function onCancel(): Promise<void> {
  if (!source || !pending.value || cancelling.value) return;
  if (!(source instanceof LiveSource)) return;
  cancelling.value = true;
  try {
    await source.cancelTurn(props.sessionId);
  } catch {
    // The cancel RPC itself failing is non-fatal: if the turn is already
    // finishing, the stream's terminal frame still resets the UI. Re-enable the
    // button so the operator can retry.
    cancelling.value = false;
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

// Rewind one CRR decision from its route-receipt chip (re-dispatch under the
// journaled class). Routes through runTurn for the same in-flight guard +
// error-banner behaviour as a normal turn; the chip disables the control for
// non-rewindable (intent-class) receipts so it never reaches here.
function onRewind(decisionId: string): void {
  if (!source) return;
  void runTurn(() => store.rewindRoute(source!, props.sessionId, decisionId));
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

.iv__reconnecting {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.25rem 1rem;
  font-size: 0.8rem;
  background: #0b1a24;
  border-bottom: 1px solid #1e4a63;
  color: var(--k-info, #7dd3fc);
}

.iv__reconnecting-dot {
  width: 0.5rem;
  height: 0.5rem;
  border-radius: 50%;
  background: currentColor;
  animation: iv-reconnecting-pulse 1s ease-in-out infinite;
}

@keyframes iv-reconnecting-pulse {
  0%,
  100% {
    opacity: 0.3;
  }
  50% {
    opacity: 1;
  }
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

.iv__focused-chat {
  flex-shrink: 0;
  padding: 0.55rem 0.8rem;
  background: #111827;
  border-bottom: 1px solid var(--k-border, #1e293b);
  display: grid;
  gap: 0.3rem;
}

.iv__focused-chat-head {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  min-width: 0;
  font-size: 0.78rem;
}

.iv__focused-chat-head strong {
  min-width: 0;
  overflow-wrap: anywhere;
}

.iv__focused-chat-label {
  color: var(--k-fg-accent, #60a5fa);
  font-size: 0.68rem;
  font-weight: 700;
  text-transform: uppercase;
}

.iv__focused-chat-close {
  margin-left: auto;
  background: transparent;
  border: 0;
  color: var(--k-fg-muted, #94a3b8);
  cursor: pointer;
  font-size: 0.7rem;
  padding: 0;
}

.iv__focused-chat-close:hover {
  color: var(--k-fg, #e2e8f0);
  text-decoration: underline;
}

.iv__focused-chat-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 0.45rem;
  color: var(--k-fg-muted, #94a3b8);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
  font-size: 0.68rem;
}

.iv__focused-chat-messages {
  display: grid;
  gap: 0.2rem;
}

.iv__focused-chat-message {
  display: grid;
  grid-template-columns: 4.5rem minmax(0, 1fr);
  gap: 0.45rem;
  font-size: 0.72rem;
}

.iv__focused-chat-role {
  color: var(--k-fg-muted, #94a3b8);
  font-weight: 650;
}

.iv__focused-chat-content {
  min-width: 0;
  color: var(--k-fg, #dbeafe);
  overflow-wrap: anywhere;
}

.iv__focused-chat-muted {
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.72rem;
}

.iv__focused-chat-error {
  color: var(--k-error, #fca5a5);
  font-size: 0.72rem;
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

/* ---- Embed layout (VS Code webview): chat ONLY ---- */
/* The chat panel shows just the conversation; Trace and Graph are their own
   dockable windows (the "Kitsoki Surfaces" panels), so the chat fills the width. */
.iv__main--embed .iv__chat {
  flex: 1 1 auto;
}
/* Let the intent-form inputs shrink below their intrinsic width so the row (and
   its Send) always fits the chat column. */
.iv__main--embed .iv__chat :deep(.input-bar__input) {
  min-width: 0;
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

.iv__thinking-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 4px;
}

.iv__thinking-role {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  opacity: 0.6;
}

.iv__thinking-stop {
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.02em;
  padding: 2px 10px;
  border-radius: 999px;
  border: 1px solid var(--k-danger-border, rgba(248, 113, 113, 0.5));
  background: var(--k-danger-bg, rgba(248, 113, 113, 0.12));
  color: var(--k-danger-fg, #fca5a5);
  cursor: pointer;
  transition: background 0.12s ease, opacity 0.12s ease;
}

.iv__thinking-stop:hover:not(:disabled) {
  background: var(--k-danger-bg-hover, rgba(248, 113, 113, 0.22));
}

.iv__thinking-stop:disabled {
  opacity: 0.55;
  cursor: default;
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
