<script setup lang="ts">
/**
 * EditorPage — the story-editor shell (docs/proposals/story-editor-shell.md +
 * oracle-workbench.md, adapted to the real backend).
 *
 * It is a PER-STORY surface (no session required): a story is selected via the
 * `?story=<id|path>` query param and a room via `?room=<id>`. The story param
 * may be a story id or the absolute app.yaml path; we resolve it against the
 * catalogue (runstatus.stories.list) to the canonical absolute path the
 * runstatus.editor.* RPCs key on.
 *
 * Layout: two columns.
 *   LEFT  — meta chat (reuses the meta store / overlay). Story-mode chats need
 *           an active session; the editor has none, so when no `?session=`
 *           query param is present we show a placeholder rather than crash.
 *   RIGHT — BFS-ordered room list (sidebar) + the selected room's detail pane
 *           (hook, domain model, typed view, oracle workbench). A reload button
 *           re-fetches the room list.
 */
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { LiveSource } from "../data/live-source.js";
import { useMetaStore } from "../stores/meta.js";
import type { StoryHeader } from "../data/live-source.js";
import type { RoomSummary, RoomDetail } from "../data/editor.js";
import HookDetail from "../components/editor/HookDetail.vue";
import DomainModel from "../components/editor/DomainModel.vue";
import OracleWorkbench from "../components/editor/OracleWorkbench.vue";
import StoryViewer from "../components/editor/StoryViewer.vue";

const route = useRoute();
const router = useRouter();
const source = new LiveSource("/");
const meta = useMetaStore();

// ── story resolution ───────────────────────────────────────────────────────
const stories = ref<StoryHeader[]>([]);
const storyParam = computed(() => String(route.query.story ?? ""));
const sessionParam = computed(() => String(route.query.session ?? ""));

/** Resolve the story query param (id or path) to the canonical app.yaml path. */
const storyPath = computed<string>(() => {
  const p = storyParam.value;
  if (!p) return "";
  const hit = stories.value.find((s) => s.path === p || s.app_id === p);
  return hit ? hit.path : p;
});

const storyTitle = computed<string>(() => {
  const hit = stories.value.find((s) => s.path === storyPath.value);
  return hit?.title || hit?.app_id || storyParam.value || "story";
});

// ── room list + selection ────────────────────────────────────────────────
const rooms = ref<RoomSummary[]>([]);
const roomsLoading = ref(false);
const roomsError = ref("");

const selectedRoomId = computed(() => String(route.query.room ?? ""));
const detail = ref<RoomDetail | null>(null);
const detailLoading = ref(false);
const detailError = ref("");

// The world snapshot a cassette replay produced, fed to the StoryViewer.
const replayWorld = ref<Record<string, unknown> | null>(null);

async function loadRooms(): Promise<void> {
  if (!storyPath.value) return;
  roomsLoading.value = true;
  roomsError.value = "";
  try {
    rooms.value = await source.editorRooms(storyPath.value);
    // Default-select the first (initial) room if none is selected.
    if (!selectedRoomId.value && rooms.value.length > 0) {
      selectRoom(rooms.value[0].id);
    }
  } catch (e) {
    roomsError.value = e instanceof Error ? e.message : String(e);
    rooms.value = [];
  } finally {
    roomsLoading.value = false;
  }
}

async function loadDetail(): Promise<void> {
  replayWorld.value = null;
  if (!storyPath.value || !selectedRoomId.value) {
    detail.value = null;
    return;
  }
  detailLoading.value = true;
  detailError.value = "";
  try {
    detail.value = await source.editorRoom(storyPath.value, selectedRoomId.value);
  } catch (e) {
    detailError.value = e instanceof Error ? e.message : String(e);
    detail.value = null;
  } finally {
    detailLoading.value = false;
  }
}

function selectRoom(roomId: string): void {
  router.replace({
    query: { ...route.query, room: roomId },
  });
}

function onReload(): void {
  loadRooms();
  loadDetail();
}

function onReplay(payload: { world: Record<string, unknown>; output: unknown }): void {
  replayWorld.value = payload.world;
}

const sourceHref = computed<string>(() => {
  const ref = detail.value?.source_ref;
  if (!ref) return "";
  return `vscode://file/${ref.path}:${ref.line}`;
});

// ── meta chat ──────────────────────────────────────────────────────────────
const metaEnabled = computed(() => sessionParam.value !== "");

async function openMetaChat(): Promise<void> {
  if (!metaEnabled.value) return;
  meta.setSession(sessionParam.value);
  await meta.loadModes(source, sessionParam.value);
  const mode = meta.modes.find((m) => m.group === "story") ?? meta.modes[0];
  if (mode) await meta.openMode(source, sessionParam.value, mode.key);
}

onMounted(async () => {
  try {
    stories.value = await source.listStories();
  } catch {
    /* catalogue best-effort; storyPath falls back to the raw param */
  }
  await loadRooms();
  await loadDetail();
});

watch(storyPath, () => {
  loadRooms();
  loadDetail();
});
watch(selectedRoomId, () => loadDetail());
</script>

<template>
  <div class="editor" data-testid="editor-page">
    <!-- LEFT: meta chat -->
    <aside class="editor__meta" data-testid="editor-meta-chat">
      <h2 class="editor__meta-title">Meta chat</h2>
      <template v-if="metaEnabled">
        <p class="editor__meta-hint">
          Editing <strong>{{ storyTitle }}</strong> with session
          <code>{{ sessionParam }}</code>.
        </p>
        <button class="editor__btn" data-testid="editor-meta-open" @click="openMetaChat">
          Open meta chat
        </button>
      </template>
      <p v-else class="editor__meta-placeholder" data-testid="editor-meta-placeholder">
        Start a session to enable meta chat.
      </p>
    </aside>

    <!-- RIGHT: room list + detail -->
    <main class="editor__main">
      <header class="editor__header">
        <h1 class="editor__title">{{ storyTitle }}</h1>
        <button class="editor__btn" data-testid="editor-reload" @click="onReload">
          ↻ Reload
        </button>
      </header>

      <div class="editor__cols">
        <!-- Room list sidebar (BFS order) -->
        <nav class="editor__rooms" data-testid="editor-room-list">
          <div v-if="roomsLoading" class="editor__status">Loading rooms…</div>
          <div v-else-if="roomsError" class="editor__status editor__status--error">
            {{ roomsError }}
          </div>
          <ul v-else class="editor__room-ul">
            <li
              v-for="r in rooms"
              :key="r.id"
              class="editor__room"
              :class="{ 'editor__room--active': r.id === selectedRoomId }"
              data-testid="editor-room-item"
              :data-room-id="r.id"
              @click="selectRoom(r.id)"
            >
              <span class="editor__room-label">{{ r.label }}</span>
              <span v-if="r.has_oracle" class="editor__room-oracle" title="Has oracle call">✦</span>
            </li>
          </ul>
        </nav>

        <!-- Room detail -->
        <section class="editor__detail" data-testid="editor-room-detail">
          <div v-if="detailLoading" class="editor__status">Loading room…</div>
          <div v-else-if="detailError" class="editor__status editor__status--error">
            {{ detailError }}
          </div>
          <p v-else-if="!detail" class="editor__status">Select a room.</p>
          <template v-else>
            <header class="editor__detail-head">
              <h2 class="editor__detail-title">{{ detail.label }}</h2>
              <a
                v-if="sourceHref"
                :href="sourceHref"
                class="editor__ide-link"
                data-testid="editor-ide-link"
                title="Open in editor"
              >Open in IDE</a>
            </header>

            <HookDetail :on-enter="detail.on_enter" />

            <DomainModel
              :world-keys="detail.world_keys"
              :intents="detail.intents"
              :transitions="detail.transitions"
              @select-room="selectRoom"
            />

            <StoryViewer
              mode="column"
              :title="`${detail.label} — view`"
              :view="{ Elements: detail.view }"
              :world-snapshot="replayWorld"
            />

            <OracleWorkbench
              :source="source"
              :story-path="storyPath"
              :room-id="detail.id"
              @replay="onReplay"
            />
          </template>
        </section>
      </div>
    </main>
  </div>
</template>

<style scoped>
.editor {
  display: grid;
  grid-template-columns: 320px 1fr;
  height: 100vh;
  overflow: hidden;
}
.editor__meta {
  border-right: 1px solid var(--border, #2a2d35);
  padding: 0.75rem;
  overflow: auto;
}
.editor__meta-title {
  margin: 0 0 0.5rem;
  font-size: 1rem;
}
.editor__meta-hint { font-size: 0.85rem; opacity: 0.85; }
.editor__meta-placeholder {
  opacity: 0.6;
  font-style: italic;
  font-size: 0.9rem;
}
.editor__main {
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.editor__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.6rem 0.9rem;
  border-bottom: 1px solid var(--border, #2a2d35);
}
.editor__title { margin: 0; font-size: 1.05rem; }
.editor__btn {
  background: #2d4a63;
  color: inherit;
  border: none;
  border-radius: 4px;
  padding: 0.3rem 0.7rem;
  cursor: pointer;
  font-size: 0.82rem;
}
.editor__cols {
  display: grid;
  grid-template-columns: 220px 1fr;
  flex: 1;
  overflow: hidden;
}
.editor__rooms {
  border-right: 1px solid var(--border, #2a2d35);
  overflow: auto;
  padding: 0.4rem;
}
.editor__room-ul {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}
.editor__room {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.35rem 0.5rem;
  border-radius: 5px;
  cursor: pointer;
  font-size: 0.88rem;
}
.editor__room:hover { background: #1c1f26; }
.editor__room--active { background: #2d4a63; }
.editor__room-label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.editor__room-oracle { margin-left: auto; color: #b39ddb; }
.editor__detail {
  overflow: auto;
  padding: 0.9rem;
  display: flex;
  flex-direction: column;
  gap: 0.9rem;
}
.editor__detail-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.editor__detail-title { margin: 0; }
.editor__ide-link {
  color: #6db3f2;
  text-decoration: none;
  font-size: 0.85rem;
}
.editor__ide-link:hover { text-decoration: underline; }
.editor__status { opacity: 0.7; font-size: 0.9rem; }
.editor__status--error { color: #f28b82; }
</style>
