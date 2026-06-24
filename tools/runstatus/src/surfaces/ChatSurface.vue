<template>
  <div class="surface" data-testid="surface-chat">
    <!-- Loading: discovering the session / hydrating. -->
    <div v-if="loading" class="surface__loading" data-testid="surface-loading">
      Loading…
    </div>

    <!-- Empty: no session yet. Chat is what STARTS sessions, so offer the
         session-start affordance here (mirrors HomeView's "New session"). -->
    <div v-else-if="!sessionId" class="surface__empty" data-testid="surface-empty">
      <p class="surface__empty-msg">Start a chat to begin.</p>
      <!-- Multi-story posture (e.g. the kitsoki repo): let the operator PICK the
           story rather than silently binding the lexicographically-first one
           (which lands on 'bugfix'). Defaults to the kitsoki-dev dogfood story
           when present so it's one click away. A single-story embed skips the
           picker entirely (the lone story is implied). -->
      <select
        v-if="stories.length > 1"
        v-model="selectedStoryPath"
        class="surface__story-select"
        data-testid="surface-story-select"
        :disabled="starting"
      >
        <option v-for="s in stories" :key="s.path" :value="s.path">
          {{ s.title || s.app_id }}
        </option>
      </select>
      <button
        type="button"
        class="surface__start"
        data-testid="surface-start-session"
        :disabled="starting || storiesLoading"
        @click="onStart"
      >
        {{ starting ? "Starting…" : "Start a chat" }}
      </button>
      <p v-if="startError" class="surface__error" data-testid="surface-start-error">
        {{ startError }}
      </p>
    </div>

    <!-- Active session: the chat column (transcript + thinking bubble + input). -->
    <template v-else>
      <header class="surface__bar">
        <span class="surface__app-id">{{ appId }}</span>
        <code class="surface__state" data-testid="current-state">{{ store.currentStatePath || "—" }}</code>
        <span
          class="surface__badge"
          data-testid="state-badge"
          :data-terminal="store.terminal ? 'true' : 'false'"
          :class="store.terminal ? 'surface__badge--done' : 'surface__badge--live'"
        >{{ store.terminal ? 'done' : 'live' }}</span>
      </header>

      <section class="surface__chat" aria-label="Conversation" data-testid="chat-section">
        <!-- chatEntries (not raw transcript) so each user turn carries its
             routing provenance and ChatTranscript can render the inline
             routing chip — same binding InteractiveView uses. -->
        <ChatTranscript
          class="surface__transcript"
          :transcript="store.chatEntries"
          @rewind="onRewind"
        />
        <!-- Streaming thinking bubble: visible while a turn is in flight. -->
        <div v-if="pending" class="surface__thinking" data-testid="thinking-bubble">
          <div class="surface__thinking-avatar">A</div>
          <div class="surface__thinking-bubble">
            <div class="surface__thinking-role">Agent</div>
            <ActivityFeed :items="store.pendingStream" />
            <div class="surface__thinking-dots"><span>·</span><span>·</span><span>·</span></div>
          </div>
        </div>
        <div v-if="store.terminal" class="surface__done-note">
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
        <div v-if="error" class="surface__error" data-testid="surface-error">{{ error }}</div>
      </section>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { useRunStore } from "../stores/run.js";
import { createDataSource } from "../data/source.js";
import type { DataSource } from "../data/source.js";
import { LiveSource } from "../data/live-source.js";
import type { StoryHeader } from "../data/live-source.js";
import ActivityFeed from "../components/ActivityFeed.vue";
import ChatTranscript from "../components/ChatTranscript.vue";
import InputBar from "../components/InputBar.vue";

const store = useRunStore();

// One DataSource for the lifetime of the surface (DI; the transport auto-selects
// Bridge in the webview / Http in the browser).
let source: DataSource | null = null;
// session.new is a session-agnostic lifecycle RPC — driven straight against the
// live server, exactly as HomeView does.
let live: LiveSource | null = null;
let unsubscribe: (() => void) | null = null;

const sessionId = ref<string | null>(null);
const loading = ref(true);
const pending = ref(false);
const error = ref<string | null>(null);

const starting = ref(false);
const startError = ref<string | null>(null);

// Discovered stories + the operator's selection, populated once when the surface
// lands in its no-session empty state. Defaults to the kitsoki-dev dogfood story
// (see ensureStories) so the picker opens on the story we actually want, never
// the lexicographically-first 'bugfix'.
const stories = ref<StoryHeader[]>([]);
const selectedStoryPath = ref<string>("");
const storiesLoading = ref(false);

const appId = computed(() => store.appDef?.id ?? store.appDef?.name ?? "kitsoki");

async function loadSession(id: string): Promise<void> {
  if (!source) return;
  await store.hydrate(source, id);
  await store.loadInitialView(source, id);
}

/** Adopt a session id from current-session discovery / subscription. */
async function adopt(id: string | null): Promise<void> {
  sessionId.value = id;
  if (id) {
    loading.value = true;
    try {
      await loadSession(id);
    } catch (e) {
      error.value = errMsg(e);
    } finally {
      loading.value = false;
    }
  } else {
    // No active session: clear the initial loading flag so the empty/start state
    // renders. Without this, `loading` (true at init) is never lowered when
    // current-session discovery returns null, leaving the surface stuck on
    // "Loading…" indefinitely instead of offering "Start a chat".
    store.teardown();
    loading.value = false;
    // Populate the story picker in the background — the empty state renders
    // immediately; the picker (and the default selection) fill in once the list
    // lands. The Start button stays disabled until then.
    void ensureStories();
  }
}

/**
 * Discover the available stories once and seed the default selection. Prefers the
 * kitsoki-dev dogfood story so the picker opens on the story the operator almost
 * always wants in the kitsoki repo; falls back to the first discovered story
 * otherwise. Idempotent — a second call while loaded/loading is a no-op.
 */
async function ensureStories(): Promise<void> {
  if (stories.value.length || storiesLoading.value) return;
  storiesLoading.value = true;
  startError.value = null;
  try {
    if (!live) live = new LiveSource("/");
    const list = await live.listStories();
    stories.value = list;
    const preferred = list.find((s) => s.app_id === "kitsoki-dev");
    selectedStoryPath.value = (preferred ?? list[0])?.path ?? "";
  } catch (e) {
    startError.value = errMsg(e);
  } finally {
    storiesLoading.value = false;
  }
}

onMounted(async () => {
  source = createDataSource();
  try {
    const current = await source.getCurrentSession();
    await adopt(current);
  } catch (e) {
    error.value = errMsg(e);
    loading.value = false;
  }

  // Re-adopt when the host switches the current session out from under us.
  unsubscribe = source.subscribeCurrentSession((id) => {
    void adopt(id);
  });
});

onUnmounted(() => {
  unsubscribe?.();
  store.teardown();
});

/**
 * Start a session for the SELECTED story (defaulted to kitsoki-dev by
 * ensureStories), then adopt the new session — the same runstatus.session.new
 * path HomeView's "New session" uses. A single-story embed has no visible picker,
 * but selectedStoryPath is still seeded to that lone story.
 */
async function onStart(): Promise<void> {
  const storyPath = selectedStoryPath.value || stories.value[0]?.path;
  if (!storyPath) {
    startError.value = "No story available to start a chat.";
    return;
  }
  starting.value = true;
  startError.value = null;
  try {
    if (!live) live = new LiveSource("/");
    const id = await live.newSession(storyPath);
    await adopt(id);
  } catch (e) {
    startError.value = errMsg(e);
  } finally {
    starting.value = false;
  }
}

async function runTurn(fn: () => Promise<unknown>): Promise<void> {
  if (pending.value || !source || store.terminal) return;
  pending.value = true;
  error.value = null;
  try {
    await fn();
  } catch (e) {
    error.value = errMsg(e);
  } finally {
    pending.value = false;
  }
}

function onSend(text: string, _intentName: string): void {
  if (!source || !sessionId.value) return;
  void runTurn(() => store.sendText(source!, sessionId.value!, text));
}

function onIntent(name: string, slots: Record<string, unknown>, displayLabel?: string): void {
  if (!source || !sessionId.value) return;
  void runTurn(() => store.submitIntent(source!, sessionId.value!, name, slots, displayLabel));
}

// Rewind one CRR decision from its route-receipt chip: reverse the route and
// re-dispatch the original utterance. Routes through runTurn so the in-flight
// guard + error banner behave exactly like a normal turn; a non-rewindable
// receipt never reaches here (the chip disables its control).
function onRewind(decisionId: string): void {
  if (!source || !sessionId.value) return;
  void runTurn(() => store.rewindRoute(source!, sessionId.value!, decisionId));
}

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
</script>

<style scoped>
.surface {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background: var(--k-bg-inset, #0f1115);
  color: var(--k-fg, #e2e8f0);
  overflow: hidden;
}

.surface__loading,
.surface__empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  height: 100%;
  color: var(--k-fg-muted, #64748b);
  font-size: 0.95rem;
}

.surface__empty-msg {
  margin: 0;
}

.surface__story-select {
  max-width: 16rem;
  padding: 0.35rem 0.5rem;
  border-radius: 0.375rem;
  border: 1px solid var(--k-border, #1e293b);
  background: var(--k-bg-input, #1e293b);
  color: var(--k-fg, #e2e8f0);
  font-size: 0.8rem;
}

.surface__start {
  background: var(--k-button-bg, #1d4ed8);
  color: var(--k-button-fg, #e2e8f0);
  border: none;
  border-radius: 0.375rem;
  padding: 0.5rem 1rem;
  font-size: 0.85rem;
  font-weight: 600;
  cursor: pointer;
}
.surface__start:hover:not(:disabled) {
  background: var(--k-button-hover-bg, #2563eb);
}
.surface__start:disabled {
  opacity: 0.5;
  cursor: default;
}

.surface__bar {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  padding: 0.5rem 1rem;
  background: var(--k-bg-widget, #0f172a);
  border-bottom: 1px solid var(--k-border, #1e293b);
  flex-shrink: 0;
  font-size: 0.8125rem;
}
.surface__app-id {
  font-weight: 600;
  color: var(--k-fg, #e2e8f0);
}
.surface__state {
  font-family: ui-monospace, monospace;
  font-size: 0.775rem;
  color: var(--k-fg-accent, #7dd3fc);
}
.surface__badge {
  display: inline-block;
  padding: 0.1rem 0.45rem;
  border-radius: 999px;
  font-size: 0.7rem;
  font-weight: 600;
}
.surface__badge--live {
  background: var(--k-success-bg, #14532d);
  color: var(--k-success, #86efac);
}
.surface__badge--done {
  background: var(--k-bg-input, #1e293b);
  color: var(--k-fg-muted, #64748b);
}

.surface__chat {
  display: flex;
  flex-direction: column;
  flex: 1 1 auto;
  min-width: 0;
  min-height: 0;
}
.surface__transcript {
  flex: 1 1 auto;
  min-height: 0;
}

.surface__done-note {
  padding: 0.6rem 1.1rem;
  font-size: 0.8rem;
  color: var(--k-fg-muted, #64748b);
  background: var(--k-bg-widget, #14171d);
  border-top: 1px solid var(--k-border-subtle, #2a2f3a);
  text-align: center;
}

.surface__error {
  padding: 0.5rem 1.1rem;
  font-size: 0.78rem;
  color: var(--k-error, #fca5a5);
}

/* ---- Streaming thinking bubble (mirrors InteractiveView) ---- */
.surface__thinking {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 8px 24px 0;
  max-width: 98%;
}
.surface__thinking-avatar {
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
.surface__thinking-bubble {
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
.surface__thinking-role {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  opacity: 0.6;
  margin-bottom: 4px;
}
.surface__thinking-dots {
  display: flex;
  gap: 4px;
  font-size: 20px;
  color: var(--k-fg-muted, #94a3b8);
}
@keyframes surface-dot-pulse {
  0%, 80%, 100% { opacity: 0.2; }
  40% { opacity: 1; }
}
.surface__thinking-dots span:nth-child(1) { animation: surface-dot-pulse 1.4s infinite 0s; }
.surface__thinking-dots span:nth-child(2) { animation: surface-dot-pulse 1.4s infinite 0.2s; }
.surface__thinking-dots span:nth-child(3) { animation: surface-dot-pulse 1.4s infinite 0.4s; }
</style>
