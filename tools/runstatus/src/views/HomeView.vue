<template>
  <div class="home" data-testid="home-view">
    <!-- ── Setup warnings ───────────────────────────────────────────────── -->
    <section
      v-if="setupWarnings.length > 0"
      class="home__setup"
      data-testid="setup-warnings"
      aria-label="Setup warnings"
    >
      <div
        v-for="warning in setupWarnings"
        :key="warning.id"
        class="home__setup-warning"
        data-testid="setup-warning"
        role="alert"
      >
        <div class="home__setup-mark" aria-hidden="true" data-testid="setup-warning-mark">!</div>
        <div class="home__setup-copy">
          <h2 class="home__setup-title" data-testid="setup-warning-title">{{ warning.title }}</h2>
          <p class="home__setup-body" data-testid="setup-warning-body">{{ warning.body }}</p>
          <code
            v-if="warning.action_command"
            class="home__setup-command"
            data-testid="setup-warning-command"
          >{{ warning.action_command }}</code>
        </div>
        <button
          v-if="setupWarningStory(warning)"
          class="home__btn home__btn--warning"
          type="button"
          data-testid="setup-warning-action"
          :disabled="startingPath === setupWarningStory(warning)?.path"
          @click="onSetupWarningAction(warning)"
        >
          {{ startingPath === setupWarningStory(warning)?.path ? "Starting…" : (warning.action_label || "Open setup story") }}
        </button>
      </div>
    </section>

    <!-- ── Stories ─────────────────────────────────────────────────────── -->
    <section class="home__section">
      <div class="home__section-head">
        <h1 class="home__title">Stories</h1>
        <button
          class="home__btn home__btn--ghost"
          data-testid="rescan-btn"
          :disabled="rescanning"
          @click="onRescan"
        >
          {{ rescanning ? "Rescanning…" : "Rescan" }}
        </button>
      </div>

      <div v-if="storiesError" class="home__status home__status--error" data-testid="stories-error">
        {{ storiesError }}
      </div>
      <div v-else-if="storiesLoading" class="home__status">Loading stories…</div>
      <div v-else-if="stories.length === 0" class="home__empty" data-testid="stories-empty">
        <p class="home__empty-title">No stories discovered yet.</p>
        <p class="home__empty-hint">
          Stories are the YAML state machines under
          <code>stories/&lt;name&gt;/</code> in your repo. Add one (or
          <button class="home__empty-link" type="button" data-testid="empty-rescan" @click="onRescan">rescan</button>
          if you just did), then start a session here.
        </p>
        <button
          class="home__btn"
          type="button"
          data-testid="take-tour-btn"
          @click="onTakeTour"
        >
          Take the tour
        </button>
      </div>
      <div v-else class="home__cards">
        <div
          v-for="story in stories"
          :key="story.path"
          class="home__card"
          data-testid="story-card"
          :data-story-path="story.path"
        >
          <div class="home__card-head">
            <span class="home__card-title" data-testid="story-title">{{ storyTitle(story) }}</span>
            <span
              v-if="story.active_sessions.length > 0"
              class="home__badge"
              data-testid="story-active-count"
              >{{ story.active_sessions.length }} live</span
            >
          </div>
          <code class="home__card-path" data-testid="story-path">{{ relativePath(story.path) }}</code>
          <div class="home__card-actions">
            <button
              class="home__btn"
              data-testid="new-session-btn"
              :disabled="startingPath === story.path"
              @click="onNewSession(story)"
            >
              {{ startingPath === story.path ? "Starting…" : "New session" }}
            </button>
            <router-link
              class="home__btn home__btn--ghost"
              data-testid="edit-story-btn"
              :to="{ path: '/editor', query: { story: story.path } }"
            >Edit story</router-link>
          </div>
          <div
            v-if="startError && startErrorPath === story.path"
            class="home__status home__status--error"
            data-testid="new-session-error"
          >
            {{ startError }}
          </div>
        </div>
      </div>
    </section>

    <!-- ── Kits (S5): a generic nav section for any installed kit's       -->
    <!-- provides.ui entries flagged nav:true (kitLoader.ts) — this view   -->
    <!-- has no idea which kits, if any, are installed. Empty (and hidden) -->
    <!-- when none are, the common case today.                            -->
    <section v-if="kitNavLinks.length > 0" class="home__section">
      <h2 class="home__subtitle">Kits</h2>
      <div class="home__cards">
        <router-link
          v-for="link in kitNavLinks"
          :key="link.path"
          class="home__btn home__btn--ghost"
          data-testid="kit-nav-link"
          :to="link.path"
        >{{ link.title }}</router-link>
      </div>
    </section>

    <!-- ── Active sessions ─────────────────────────────────────────────── -->
    <section class="home__section">
      <h2 class="home__subtitle">Active sessions</h2>

      <!-- Filter chips -->
      <div class="home__session-filters">
        <button
          class="home__filter-chip"
          :class="{ 'home__filter-chip--active': sessionFilter === 'all' }"
          data-testid="session-filter-all"
          @click="sessionFilter = 'all'"
        >All</button>
        <button
          class="home__filter-chip"
          :class="{ 'home__filter-chip--active': sessionFilter === 'active' }"
          data-testid="session-filter-active"
          @click="sessionFilter = 'active'"
        >Active</button>
        <button
          class="home__filter-chip"
          :class="{ 'home__filter-chip--active': sessionFilter === 'terminal' }"
          data-testid="session-filter-terminal"
          @click="sessionFilter = 'terminal'"
        >Terminal</button>
      </div>

      <div v-if="sessionsError" class="home__status home__status--error" data-testid="sessions-error">
        {{ sessionsError }}
      </div>
      <div v-else-if="sessions.length === 0" class="home__status" data-testid="sessions-empty">
        No live sessions.
      </div>
      <div v-else-if="filteredSessions.length === 0" class="home__status" data-testid="sessions-empty-filtered">
        No sessions match the current filter.
      </div>
      <table v-else class="home__table" data-testid="session-table">
        <thead>
          <tr>
            <th
              class="home__th--sortable"
              data-testid="session-sort-story"
              @click="toggleSort('story')"
            >
              Story
              <span class="home__sort-indicator">{{ sortIndicator('story') }}</span>
            </th>
            <th>Session</th>
            <th
              class="home__th--sortable"
              data-testid="session-sort-state"
              @click="toggleSort('state')"
            >
              State
              <span class="home__sort-indicator">{{ sortIndicator('state') }}</span>
            </th>
            <th>Operation</th>
            <th
              class="home__th--sortable"
              data-testid="session-sort-activity"
              @click="toggleSort('activity')"
            >
              Activity
              <span class="home__sort-indicator">{{ sortIndicator('activity') }}</span>
            </th>
            <th data-testid="session-sort-turns">Turns</th>
            <th>Duration</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="s in filteredSessions"
            :key="s.session_id"
            class="home__row"
            data-testid="session-row"
            :data-session-id="s.session_id"
          >
            <td>
              <div class="home__row-story">{{ sessionStoryTitle(s) }}</div>
              <code class="home__row-path">{{ sessionStoryPath(s) }}</code>
            </td>
            <td><code data-testid="session-id">{{ truncateId(s.session_id) }}</code></td>
            <td><code data-testid="session-state">{{ s.current_state }}</code></td>
            <td
              class="home__row-operation"
              data-testid="session-operation"
              :data-operation-status="s.operation_run?.status || ''"
            >
              <div v-if="s.operation_run" class="home__operation">
                <div class="home__operation-line">
                  <span class="home__operation-title" data-testid="session-operation-title">
                    {{ operationTitle(s) }}
                  </span>
                  <span
                    class="home__operation-status"
                    :class="operationStatusClass(s)"
                    data-testid="session-operation-status"
                  >
                    {{ operationStatusLabel(s) }}
                  </span>
                </div>
                <div
                  v-if="operationDetail(s)"
                  class="home__operation-detail"
                  data-testid="session-operation-detail"
                >
                  {{ operationDetail(s) }}
                </div>
                <div
                  v-if="operationFacts(s).length > 0"
                  class="home__operation-summary"
                  data-testid="session-operation-summary"
                >
                  <span
                    v-for="fact in operationFacts(s)"
                    :key="fact.label"
                    class="home__operation-fact"
                  >
                    <span class="home__operation-fact-label">{{ fact.label }}</span>
                    {{ fact.value }}
                  </span>
                </div>
              </div>
              <span v-else class="home__row-muted">—</span>
            </td>
            <td class="home__row-activity" data-testid="session-activity">{{ formatDate(s.started_at) }}</td>
            <td class="home__row-turns" data-testid="session-turns">{{ s.turn != null ? s.turn : '—' }}</td>
            <td class="home__row-duration" data-testid="session-duration">—</td>
            <td class="home__row-actions">
              <a
                v-if="operationArtifactHref(s)"
                class="home__link"
                data-testid="session-operation-artifact-open"
                :href="operationArtifactHref(s)"
                target="_blank"
                rel="noopener noreferrer"
                :title="`Open ${operationArtifactLabel(s)}`"
              >Artifact</a>
              <button
                v-if="canDriveOperation(s)"
                class="home__link home__link--button"
                type="button"
                data-testid="session-drive-operation"
                :disabled="drivingSession === s.session_id"
                @click="onDriveOperation(s)"
              >
                {{ drivingSession === s.session_id ? "Driving" : "Drive" }}
              </button>
              <router-link
                class="home__link"
                data-testid="session-open"
                :to="`/s/${s.session_id}`"
                >Open</router-link
              >
            </td>
          </tr>
        </tbody>
      </table>
    </section>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from "vue";
import { useRouter } from "vue-router";
// Auto-navigate fires at most once per browser tab — see lib/auto-nav for the
// full rationale (persisted in sessionStorage; also marked spent by the session
// views so a tab that opens straight into a session can still reach "/").
import { autoNavDone, markAutoNavDone } from "../lib/auto-nav.js";
import { LiveSource, type SetupWarning, type StoryHeader } from "../data/live-source.js";
import { createDataSource } from "../data/source.js";
import type { SessionHeader } from "../types.js";
import { useTourStore } from "../stores/tour.js";
import { kitNavLinks } from "../kits/kitLoader.js";

// The home screen drives the session-agnostic lifecycle RPCs directly against
// the live server. In a static snapshot artifact (file:// trace-review mode)
// there is no server and no story catalogue — just one captured session — so
// the `/` entry instead behaves like the former SessionList: it reads the one
// session from the snapshot and navigates straight into its observer view.
// Live reads refresh on a short poll interval rather than fsnotify (the
// explicit-rescan lean).
const POLL_MS = 3000;

function snapshotSession(): unknown {
  return (globalThis as typeof globalThis & { __KITSOKI_SNAPSHOT__?: unknown })
    .__KITSOKI_SNAPSHOT__;
}

// A live session opens on its drive (chat) surface — that is where the operator
// acts; a terminal one opens on the read-only observer.
function sessionRoute(s: SessionHeader): string {
  return s.terminal ? `/s/${s.session_id}` : `/s/${s.session_id}/chat`;
}

const router = useRouter();
const source = new LiveSource("/");

const stories = ref<StoryHeader[]>([]);
const storiesLoading = ref(true);
const storiesError = ref<string | null>(null);
const rescanning = ref(false);
const setupWarnings = ref<SetupWarning[]>([]);

const sessions = ref<SessionHeader[]>([]);
const sessionsError = ref<string | null>(null);

const startingPath = ref<string | null>(null);
const startError = ref<string | null>(null);

type OperationFact = { label: string; value: string };
const startErrorPath = ref<string | null>(null);
const drivingSession = ref<string | null>(null);

// ── Session table: filter + sort ─────────────────────────────────────────────
type SessionFilterMode = "all" | "active" | "terminal";
type SortKey = "story" | "state" | "activity";
type SortDir = "asc" | "desc" | null;

const sessionFilter = ref<SessionFilterMode>("all");
const sortKey = ref<SortKey | null>(null);
const sortDir = ref<SortDir>(null);

function toggleSort(key: SortKey): void {
  if (sortKey.value !== key) {
    sortKey.value = key;
    sortDir.value = "asc";
  } else if (sortDir.value === "asc") {
    sortDir.value = "desc";
  } else {
    sortKey.value = null;
    sortDir.value = null;
  }
}

function sortIndicator(key: SortKey): string {
  if (sortKey.value !== key) return "";
  return sortDir.value === "asc" ? " ▲" : " ▼";
}

const filteredSessions = computed(() => {
  let list = sessions.value.slice();

  // Apply filter
  if (sessionFilter.value === "active") {
    list = list.filter((s) => !s.terminal);
  } else if (sessionFilter.value === "terminal") {
    list = list.filter((s) => s.terminal);
  }

  // Apply sort
  if (sortKey.value) {
    const key = sortKey.value;
    const dir = sortDir.value === "desc" ? -1 : 1;
    list.sort((a, b) => {
      let av = "";
      let bv = "";
      if (key === "story") {
        av = sessionStoryTitle(a);
        bv = sessionStoryTitle(b);
      } else if (key === "state") {
        av = a.current_state;
        bv = b.current_state;
      } else if (key === "activity") {
        av = a.started_at;
        bv = b.started_at;
      }
      return dir * av.localeCompare(bv);
    });
  }

  return list;
});

let pollTimer: ReturnType<typeof setInterval> | null = null;

onMounted(async () => {
  // Snapshot / artifact mode (file://): no live server. Read the single
  // captured session from the snapshot source and open its observer view, so
  // the trace-review viewer keeps working from the `/` entry. The live
  // lifecycle RPCs (stories.list, sessions.list, …) are never attempted here.
  if (snapshotSession() !== undefined) {
    storiesLoading.value = false;
    try {
      const list = await createDataSource().listSessions();
      if (list[0]) {
        router.replace(`/s/${list[0].session_id}`);
        return;
      }
    } catch (e) {
      sessionsError.value = errMsg(e);
    }
    return;
  }

  await Promise.all([loadStories(), loadSessions(), loadSetupWarnings()]);
  storiesLoading.value = false;

  // Auto-navigate when there is exactly one live session and no others. A
  // still-running session opens on its drive (chat) surface so the operator can
  // act immediately; a finished one opens on the read-only observer.
  // Guard: only auto-navigate once per browser session — subsequent arrivals at
  // "/" are intentional (e.g. the user clicked "← Stories" to get back here).
  const only = sessions.value[0];
  if (!autoNavDone() && sessions.value.length === 1 && only) {
    markAutoNavDone();
    router.replace(sessionRoute(only));
    return;
  }
  markAutoNavDone();

  pollTimer = setInterval(() => {
    void loadSessions();
  }, POLL_MS);
});

onUnmounted(() => {
  if (pollTimer !== null) clearInterval(pollTimer);
});

async function loadStories(): Promise<void> {
  try {
    stories.value = await source.listStories();
    storiesError.value = null;
  } catch (e) {
    storiesError.value = errMsg(e);
  }
}

async function loadSessions(): Promise<void> {
  try {
    sessions.value = await source.listSessions();
    sessionsError.value = null;
  } catch (e) {
    sessionsError.value = errMsg(e);
  }
}

async function loadSetupWarnings(): Promise<void> {
  try {
    const status = await source.setupStatus();
    setupWarnings.value = status.warnings ?? [];
  } catch {
    setupWarnings.value = [];
  }
}

async function onRescan(): Promise<void> {
  rescanning.value = true;
  try {
    stories.value = await source.rescanStories();
    storiesError.value = null;
  } catch (e) {
    storiesError.value = errMsg(e);
  } finally {
    rescanning.value = false;
  }
}

async function onNewSession(story: StoryHeader): Promise<void> {
  startingPath.value = story.path;
  startError.value = null;
  startErrorPath.value = null;
  try {
    const id = await source.newSession(story.path);
    // A freshly created session is live and meant to be driven — open it on the
    // chat surface so the next action (the opening prompt) is right there.
    router.push(`/s/${id}/chat`);
  } catch (e) {
    // Fail fast: surface the structured error in place rather than navigating.
    startError.value = errMsg(e);
    startErrorPath.value = story.path;
  } finally {
    startingPath.value = null;
  }
}

async function onSetupWarningAction(warning: SetupWarning): Promise<void> {
  const story = setupWarningStory(warning);
  if (!story) return;
  await onNewSession(story);
}

async function onDriveOperation(s: SessionHeader): Promise<void> {
  if (!canDriveOperation(s) || drivingSession.value !== null) return;
  drivingSession.value = s.session_id;
  sessionsError.value = null;
  try {
    await source.driveOperation(s.session_id);
    await loadSessions();
  } catch (e) {
    sessionsError.value = errMsg(e);
  } finally {
    drivingSession.value = null;
  }
}

// Getting-started CTA on the stories-empty branch: replay the onboarding tour
// so a first-time developer has a next step instead of a dead end. Resolved
// lazily (not at setup) so the store is only required when the empty state is
// actually present and clicked. `replay` so the tour runs even if previously
// completed.
function onTakeTour(): void {
  useTourStore().start(true);
}

function storyTitle(story: StoryHeader): string {
  return story.title || story.app_id || relativePath(story.path);
}

function setupWarningStory(warning: SetupWarning): StoryHeader | undefined {
  const storyID = warning.story_id || storyIDFromRef(warning.story_ref);
  if (!storyID) return undefined;
  return stories.value.find((st) =>
    st.app_id === storyID || st.path.includes(`/stories/${storyID}/`)
  );
}

function storyIDFromRef(ref?: string): string {
  if (!ref) return "";
  return ref.replace(/^@kitsoki\//, "").trim();
}

function sessionStoryTitle(s: SessionHeader): string {
  const story = stories.value.find((st) =>
    st.active_sessions.includes(s.session_id)
  );
  return story ? storyTitle(story) : s.app_id;
}

function sessionStoryPath(s: SessionHeader): string {
  const story = stories.value.find((st) =>
    st.active_sessions.includes(s.session_id)
  );
  return story ? relativePath(story.path) : "";
}

function operationTitle(s: SessionHeader): string {
  const run = s.operation_run;
  if (!run) return "";
  return run.title || run.operation_id || run.policy_id || "operation";
}

function operationStatusLabel(s: SessionHeader): string {
  const run = s.operation_run;
  if (!run) return "";
  const status = run.status || "running";
  if (status === "waiting" && run.stop_reason) return `waiting for ${run.stop_reason}`;
  if (status === "running" && run.run_in_background) return "running in background";
  return status.replace(/_/g, " ");
}

function operationStatusClass(s: SessionHeader): string {
  const status = s.operation_run?.status || "running";
  return `home__operation-status--${status.replace(/[^a-z0-9_-]/gi, "-")}`;
}

function canDriveOperation(s: SessionHeader): boolean {
  const run = s.operation_run;
  if (!run) return false;
  return (
    !s.terminal &&
    (run.status === "" || run.status === undefined || run.status === "running") &&
    operationModeCanDrive(run.mode)
  );
}

function operationModeCanDrive(mode?: string): boolean {
  return !mode || mode === "autonomous" || mode === "supervised";
}

function operationDetail(s: SessionHeader): string {
  const run = s.operation_run;
  if (!run) return "";
  if (run.stop_detail) return run.stop_detail;
  if (run.status === "waiting" && run.terminal_state) return `parked at ${run.terminal_state}`;
  if (run.status === "completed" && run.terminal_state) return `terminal ${run.terminal_state}`;
  if (run.terminal_artifact) return `artifact ${run.terminal_artifact}`;
  if (run.phase) return `phase ${operationPhaseLabel(run.phase)}`;
  if (run.from && run.to) return `${run.from} -> ${run.to}`;
  return run.entry_intent ? `intent ${run.entry_intent}` : "";
}

function operationFacts(s: SessionHeader): OperationFact[] {
  const run = s.operation_run;
  if (!run) return [];
  const facts: OperationFact[] = [];
  const add = (label: string, value?: string) => {
    if (value && value.trim()) facts.push({ label, value });
  };
  add("mode", run.mode);
  add("execution", run.execution_mode);
  if (run.phase) add("phase", operationPhaseLabel(run.phase));
  if (run.from && run.to) add("route", `${run.from} -> ${run.to}`);
  add("intent", run.entry_intent);
  add("terminal", run.terminal_state);
  add("artifact", run.terminal_artifact);
  add("stop", run.stop_reason);
  return facts;
}

function operationArtifactHref(s: SessionHeader): string {
  const artifact = operationArtifactHandle(s);
  return artifact ? source.artifactUrl(artifact) : "";
}

function operationArtifactHandle(s: SessionHeader): string {
  const run = s.operation_run;
  return run?.terminal_artifact_handle || run?.terminal_artifact || "";
}

function operationArtifactLabel(s: SessionHeader): string {
  const run = s.operation_run;
  return run?.terminal_artifact || run?.terminal_artifact_handle || "artifact";
}

function operationPhaseLabel(phase: string): string {
  return phase.trim().replace(/_artifact$/i, "").replace(/_/g, " ");
}

function relativePath(abs: string): string {
  // Display-only: strip a leading cwd-ish prefix to a story-relative tail
  // (…/stories/<rest>) when present, else show the basename's parent chain.
  const m = abs.match(/stories\/.*/);
  if (m) return m[0];
  const parts = abs.split("/");
  return parts.slice(-2).join("/");
}

function truncateId(id: string): string {
  return id.length > 8 ? id.slice(0, 8) : id;
}

function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}
</script>

<style scoped>
.home {
  padding: 1.5rem;
  max-width: 900px;
  margin: 0 auto;
}

.home__setup {
  margin-bottom: 1.25rem;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.home__setup-warning {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: flex-start;
  gap: 1rem;
  border: 1px solid color-mix(in srgb, #f97316 72%, #ef4444);
  background: color-mix(in srgb, #f97316 22%, var(--k-bg-widget, #111827));
  border-radius: 0.5rem;
  padding: 1rem;
  box-shadow: inset 4px 0 0 color-mix(in srgb, #f97316 70%, #ef4444);
}

.home__setup-mark {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.4rem;
  height: 1.4rem;
  border-radius: 999px;
  background: color-mix(in srgb, #f97316 76%, #ef4444);
  color: #111827;
  font-size: 0.95rem;
  font-weight: 900;
  line-height: 1;
}

.home__setup-copy {
  min-width: 0;
}

.home__setup-title {
  color: #fdba74;
  font-size: 0.95rem;
  font-weight: 700;
  margin-bottom: 0.35rem;
}

.home__setup-body {
  color: var(--k-fg-muted, #cbd5e1);
  font-size: 0.85rem;
  line-height: 1.45;
  margin-bottom: 0.55rem;
}

.home__setup-command {
  display: inline-block;
  max-width: 100%;
  color: #fed7aa;
  font-size: 0.76rem;
  white-space: normal;
  word-break: break-word;
}

.home__section {
  margin-bottom: 2rem;
}

.home__section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 1rem;
}

.home__title {
  font-size: 1.25rem;
  font-weight: 600;
  color: var(--k-fg, #e2e8f0);
}

.home__subtitle {
  font-size: 1rem;
  font-weight: 600;
  color: #cbd5e1;
  margin-bottom: 1rem;
}

.home__status {
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.875rem;
  padding: 0.5rem 0;
}

.home__status--error {
  color: var(--k-error, #f87171);
}

.home__empty {
  background: var(--k-bg-widget, #111827);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 0.5rem;
  padding: 1.25rem;
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 0.6rem;
}

.home__empty-title {
  font-weight: 600;
  color: var(--k-fg, #e2e8f0);
}

.home__empty-hint {
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.85rem;
  line-height: 1.5;
}

.home__empty-hint code {
  color: var(--k-fg-code, #7dd3fc);
}

.home__empty-link {
  background: none;
  border: none;
  padding: 0;
  font: inherit;
  color: var(--k-fg-accent, #60a5fa);
  cursor: pointer;
  text-decoration: underline;
}

.home__cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 1rem;
}

.home__card {
  background: var(--k-bg-widget, #111827);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 0.5rem;
  padding: 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.home__card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
}

.home__card-title {
  font-weight: 600;
  color: var(--k-fg, #e2e8f0);
}

.home__badge {
  display: inline-block;
  padding: 0.1rem 0.45rem;
  border-radius: 999px;
  font-size: 0.7rem;
  font-weight: 600;
  background: var(--k-success-bg, #14532d);
  color: var(--k-success, #86efac);
  white-space: nowrap;
}

.home__card-path {
  font-family: ui-monospace, monospace;
  font-size: 0.75rem;
  color: var(--k-fg-code, #7dd3fc);
  word-break: break-all;
}

.home__card-actions {
  margin-top: 0.25rem;
}

.home__btn {
  background: var(--k-button-bg, #1d4ed8);
  color: var(--k-button-fg, #e2e8f0);
  border: none;
  border-radius: 0.375rem;
  padding: 0.4rem 0.8rem;
  font-size: 0.8rem;
  font-weight: 600;
  cursor: pointer;
}

.home__btn:hover:not(:disabled) {
  background: var(--k-button-hover-bg, #2563eb);
}

.home__btn:disabled {
  opacity: 0.5;
  cursor: default;
}

.home__btn--ghost {
  background: transparent;
  border: 1px solid var(--k-border-subtle, #334155);
  color: #cbd5e1;
}

.home__btn--ghost:hover:not(:disabled) {
  background: var(--k-bg-hover, #1e293b);
}

.home__btn--warning {
  flex: 0 0 auto;
  background: #f97316;
  color: #111827;
}

.home__btn--warning:hover:not(:disabled) {
  background: #fb923c;
}

@media (max-width: 640px) {
  .home__setup-warning {
    grid-template-columns: auto minmax(0, 1fr);
  }

  .home__btn--warning {
    grid-column: 1 / -1;
    width: 100%;
  }
}

/* ── Session filter chips ─────────────────────────────────────────────────── */
.home__session-filters {
  display: flex;
  gap: 0.4rem;
  margin-bottom: 0.75rem;
}

.home__filter-chip {
  background: transparent;
  border: 1px solid var(--k-border-subtle, #334155);
  color: var(--k-fg-muted, #64748b);
  border-radius: 999px;
  padding: 0.15rem 0.6rem;
  font-size: 0.75rem;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
  transition: color 0.1s, border-color 0.1s, background 0.1s;
}

.home__filter-chip:hover {
  color: var(--k-fg-muted, #94a3b8);
  border-color: var(--k-fg-subtle, #475569);
  background: var(--k-bg-hover, #1e293b);
}

.home__filter-chip--active {
  color: var(--k-fg-accent, #60a5fa);
  border-color: var(--k-button-bg, #1d4ed8);
  background: rgba(29, 78, 216, 0.12);
}

.home__table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.875rem;
}

.home__table th {
  text-align: left;
  color: var(--k-fg-muted, #64748b);
  border-bottom: 1px solid var(--k-border, #1e293b);
  padding: 0.4rem 0.6rem;
  font-weight: 600;
  font-size: 0.75rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.home__th--sortable {
  cursor: pointer;
  user-select: none;
}

.home__th--sortable:hover {
  color: var(--k-fg-muted, #94a3b8);
}

.home__sort-indicator {
  font-size: 0.65rem;
  opacity: 0.8;
}

.home__table td {
  color: var(--k-fg, #e2e8f0);
  padding: 0.5rem 0.6rem;
  border-bottom: 1px solid var(--k-border, #1a2337);
  vertical-align: top;
}

.home__row-story {
  color: var(--k-fg, #e2e8f0);
  font-weight: 500;
}

.home__row-path {
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  color: var(--k-fg-muted, #64748b);
}

.home__row-activity {
  color: var(--k-fg-muted, #94a3b8);
  white-space: nowrap;
}

.home__row-turns {
  color: var(--k-fg-muted, #94a3b8);
  font-family: ui-monospace, monospace;
  font-size: 0.8rem;
}

.home__row-duration {
  color: var(--k-fg-muted, #64748b);
  font-family: ui-monospace, monospace;
  font-size: 0.8rem;
}

.home__row-operation {
  min-width: 13rem;
  max-width: 22rem;
}

.home__operation {
  min-width: 0;
}

.home__operation-line {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  min-width: 0;
}

.home__operation-title {
  min-width: 0;
  max-width: 12rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--k-fg, #e2e8f0);
  font-weight: 600;
}

.home__operation-status {
  flex: 0 0 auto;
  border-radius: 999px;
  padding: 0.08rem 0.42rem;
  font-size: 0.68rem;
  font-weight: 700;
  white-space: nowrap;
}

.home__operation-status--running {
  color: #7dd3fc;
  border: 1px solid color-mix(in srgb, #38bdf8 42%, transparent);
  background: rgba(14, 165, 233, 0.12);
}

.home__operation-status--waiting {
  color: #facc15;
  border: 1px solid color-mix(in srgb, #facc15 42%, transparent);
  background: rgba(250, 204, 21, 0.1);
}

.home__operation-status--completed {
  color: #86efac;
  border: 1px solid color-mix(in srgb, #22c55e 42%, transparent);
  background: rgba(34, 197, 94, 0.12);
}

.home__operation-detail {
  margin-top: 0.2rem;
  max-width: 100%;
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.73rem;
  line-height: 1.3;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.home__operation-summary {
  margin-top: 0.32rem;
  display: flex;
  flex-wrap: wrap;
  gap: 0.24rem;
  min-width: 0;
}

.home__operation-fact {
  display: inline-flex;
  align-items: baseline;
  gap: 0.2rem;
  max-width: 100%;
  border: 1px solid var(--k-border-subtle, #334155);
  border-radius: 4px;
  padding: 0.06rem 0.3rem;
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.68rem;
  line-height: 1.25;
  overflow-wrap: anywhere;
}

.home__operation-fact-label {
  color: var(--k-fg-subtle, #64748b);
  font-size: 0.58rem;
  font-weight: 700;
  letter-spacing: 0;
  text-transform: uppercase;
}

.home__row-muted {
  color: var(--k-fg-muted, #64748b);
}

.home__row-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 0.55rem;
  white-space: nowrap;
}

.home__link {
  color: var(--k-fg-accent, #60a5fa);
  text-decoration: none;
  font-size: 0.8rem;
  font-weight: 600;
}

.home__link--button {
  appearance: none;
  background: none;
  border: none;
  cursor: pointer;
  font: inherit;
  padding: 0;
}

.home__link--button:disabled {
  cursor: default;
  opacity: 0.55;
}

.home__link:hover {
  text-decoration: underline;
}

code {
  font-family: ui-monospace, monospace;
}
</style>
