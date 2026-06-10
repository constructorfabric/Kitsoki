<template>
  <div class="home" data-testid="home-view">
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
      <div v-else-if="stories.length === 0" class="home__status" data-testid="stories-empty">
        No stories discovered.
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
            <td class="home__row-activity" data-testid="session-activity">{{ formatDate(s.started_at) }}</td>
            <td class="home__row-turns" data-testid="session-turns">{{ s.turn != null ? s.turn : '—' }}</td>
            <td class="home__row-duration" data-testid="session-duration">—</td>
            <td class="home__row-actions">
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
import { LiveSource, type StoryHeader } from "../data/live-source.js";
import { createDataSource } from "../data/source.js";
import type { SessionHeader } from "../types.js";

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

const sessions = ref<SessionHeader[]>([]);
const sessionsError = ref<string | null>(null);

const startingPath = ref<string | null>(null);
const startError = ref<string | null>(null);
const startErrorPath = ref<string | null>(null);

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

  await Promise.all([loadStories(), loadSessions()]);
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

function storyTitle(story: StoryHeader): string {
  return story.title || story.app_id || relativePath(story.path);
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
  color: #e2e8f0;
}

.home__subtitle {
  font-size: 1rem;
  font-weight: 600;
  color: #cbd5e1;
  margin-bottom: 1rem;
}

.home__status {
  color: #94a3b8;
  font-size: 0.875rem;
  padding: 0.5rem 0;
}

.home__status--error {
  color: #f87171;
}

.home__cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 1rem;
}

.home__card {
  background: #111827;
  border: 1px solid #1e293b;
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
  color: #e2e8f0;
}

.home__badge {
  display: inline-block;
  padding: 0.1rem 0.45rem;
  border-radius: 999px;
  font-size: 0.7rem;
  font-weight: 600;
  background: #14532d;
  color: #86efac;
  white-space: nowrap;
}

.home__card-path {
  font-family: ui-monospace, monospace;
  font-size: 0.75rem;
  color: #7dd3fc;
  word-break: break-all;
}

.home__card-actions {
  margin-top: 0.25rem;
}

.home__btn {
  background: #1d4ed8;
  color: #e2e8f0;
  border: none;
  border-radius: 0.375rem;
  padding: 0.4rem 0.8rem;
  font-size: 0.8rem;
  font-weight: 600;
  cursor: pointer;
}

.home__btn:hover:not(:disabled) {
  background: #2563eb;
}

.home__btn:disabled {
  opacity: 0.5;
  cursor: default;
}

.home__btn--ghost {
  background: transparent;
  border: 1px solid #334155;
  color: #cbd5e1;
}

.home__btn--ghost:hover:not(:disabled) {
  background: #1e293b;
}

/* ── Session filter chips ─────────────────────────────────────────────────── */
.home__session-filters {
  display: flex;
  gap: 0.4rem;
  margin-bottom: 0.75rem;
}

.home__filter-chip {
  background: transparent;
  border: 1px solid #334155;
  color: #64748b;
  border-radius: 999px;
  padding: 0.15rem 0.6rem;
  font-size: 0.75rem;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
  transition: color 0.1s, border-color 0.1s, background 0.1s;
}

.home__filter-chip:hover {
  color: #94a3b8;
  border-color: #475569;
  background: #1e293b;
}

.home__filter-chip--active {
  color: #60a5fa;
  border-color: #1d4ed8;
  background: rgba(29, 78, 216, 0.12);
}

.home__table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.875rem;
}

.home__table th {
  text-align: left;
  color: #64748b;
  border-bottom: 1px solid #1e293b;
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
  color: #94a3b8;
}

.home__sort-indicator {
  font-size: 0.65rem;
  opacity: 0.8;
}

.home__table td {
  color: #e2e8f0;
  padding: 0.5rem 0.6rem;
  border-bottom: 1px solid #1a2337;
  vertical-align: top;
}

.home__row-story {
  color: #e2e8f0;
  font-weight: 500;
}

.home__row-path {
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  color: #64748b;
}

.home__row-activity {
  color: #94a3b8;
  white-space: nowrap;
}

.home__row-turns {
  color: #94a3b8;
  font-family: ui-monospace, monospace;
  font-size: 0.8rem;
}

.home__row-duration {
  color: #64748b;
  font-family: ui-monospace, monospace;
  font-size: 0.8rem;
}

.home__row-actions {
  text-align: right;
  white-space: nowrap;
}

.home__link {
  color: #60a5fa;
  text-decoration: none;
  font-size: 0.8rem;
  font-weight: 600;
}

.home__link:hover {
  text-decoration: underline;
}

code {
  font-family: ui-monospace, monospace;
}
</style>
