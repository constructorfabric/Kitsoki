<script setup lang="ts">
/**
 * ReviewPage — the /review feedback surface (proposal:
 * docs/proposals/video-feedback-mode.md). Two columns:
 *
 *   left  — the video player + chapter timeline + flag list
 *   right — the selected flag's still, source_ref, per-flag chat, dispatch
 *
 * It owns the local flag store and the per-flag chat transcripts; the panel
 * captures and dispatches feedback notes (epic shared decision 3) and never
 * edits a spec. Chapters come from runstatus.video.chapters; stills from
 * runstatus.video.frame; dispatch via runstatus.feedback.add; the chat reuses
 * the read-only off-path oracle.
 *
 * Route: /review/:sessionId?video=<handle>. The video player + still are media
 * elements resolved through the DataSource.artifactUrl resolver.
 */
import { ref, computed, onMounted, reactive } from "vue";
import { useRoute } from "vue-router";
import { createDataSource } from "../data/source.js";
import type { Chapter } from "../data/source.js";
import type { Flag } from "../lib/flags.js";
import { dominantChapter } from "../lib/flags.js";
import ChapterTimeline from "../components/ChapterTimeline.vue";
import FlagList from "../components/FlagList.vue";
import FlagDetail from "../components/FlagDetail.vue";

const props = defineProps<{ sessionId: string }>();

const route = useRoute();
const ds = createDataSource();

const video = computed(() => (route.query.video as string) ?? "");

const chapters = ref<Chapter[]>([]);
const loadError = ref<string | null>(null);
const player = ref<HTMLVideoElement | null>(null);
const selection = ref<{ start_ms: number; end_ms: number } | null>(null);

const flags = ref<Flag[]>([]);
const selectedId = ref<number | null>(null);
let nextFlagId = 1;

// Per-flag chat transcripts, keyed by flag id.
const chats = reactive<Record<number, { role: string; text: string }[]>>({});
const chatBusy = ref(false);

const selectedFlag = computed(
  () => flags.value.find((f) => f.id === selectedId.value) ?? null
);

// Total duration: prefer the last chapter's end, fall back to the player's
// metadata once it loads (chapters may carry zero-width windows — slice-1
// deviation 2 — in which case ChapterTimeline spaces markers evenly).
const playerDurationMs = ref(0);
const totalMs = computed(() => {
  const fromChapters = chapters.value.reduce(
    (m, c) => Math.max(m, c.end_ms),
    0
  );
  return Math.max(fromChapters, playerDurationMs.value);
});

onMounted(async () => {
  if (!video.value) {
    loadError.value = "No video handle — open /review with ?video=<handle>.";
    return;
  }
  try {
    chapters.value = await ds.videoChapters(props.sessionId, video.value);
  } catch (e) {
    // Missing sidecar / no chapters degrades gracefully (proposal); other
    // errors surface for the operator.
    chapters.value = [];
    loadError.value = e instanceof Error ? e.message : String(e);
  }
});

function videoUrl(): string {
  return ds.artifactUrl(video.value);
}

function onLoadedMetadata() {
  if (player.value && !Number.isNaN(player.value.duration)) {
    playerDurationMs.value = Math.round(player.value.duration * 1000);
  }
}

function onSeek(tMs: number) {
  selection.value = { start_ms: tMs, end_ms: tMs };
  if (player.value) player.value.currentTime = tMs / 1000;
}

function onSelect(range: { start_ms: number; end_ms: number }) {
  selection.value = range;
  if (player.value) player.value.currentTime = range.start_ms / 1000;
}

async function onFlag(range: { start_ms: number; end_ms: number }) {
  const chapter = dominantChapter(chapters.value, range.start_ms, range.end_ms);
  const flag: Flag = {
    id: nextFlagId++,
    start_ms: range.start_ms,
    end_ms: range.end_ms,
    chapter,
    frame_handle: null,
    instruction: "",
    sent: false,
  };
  flags.value.push(flag);
  chats[flag.id] = [];
  selectedId.value = flag.id;

  // Eager still capture (proposal open-question 3: grab on-flag so the note
  // always carries a frame_handle). Mutate through the reactive array entry
  // (not the local const) so the still renders.
  try {
    const { handle } = await ds.videoFrame(
      props.sessionId,
      video.value,
      range.start_ms
    );
    const live = flags.value.find((f) => f.id === flag.id);
    if (live) live.frame_handle = handle;
  } catch {
    // Still capture failed (e.g. ffmpeg absent) — the flag still works, the
    // note just carries no frame_handle.
  }
}

function onSelectFlag(id: number) {
  selectedId.value = id;
}

function onUpdateInstruction(value: string) {
  if (selectedFlag.value) selectedFlag.value.instruction = value;
}

async function onSendChat(input: string) {
  const f = selectedFlag.value;
  if (!f) return;
  chats[f.id].push({ role: "user", text: input });
  chatBusy.value = true;
  try {
    const { answer } = await ds.offpath(props.sessionId, input);
    chats[f.id].push({ role: "assistant", text: answer });
  } catch (e) {
    chats[f.id].push({
      role: "assistant",
      text: `(chat unavailable: ${e instanceof Error ? e.message : String(e)})`,
    });
  } finally {
    chatBusy.value = false;
  }
}

async function dispatchFlag(f: Flag) {
  await ds.addFeedback(props.sessionId, {
    video: video.value,
    source_ref: f.chapter?.source_ref,
    time_range:
      f.end_ms > f.start_ms
        ? { start_ms: f.start_ms, end_ms: f.end_ms }
        : { start_ms: f.start_ms },
    frame_handle: f.frame_handle ?? undefined,
    instruction: f.instruction.trim(),
  });
  f.sent = true;
}

async function onSendRefine() {
  const f = selectedFlag.value;
  if (f) await dispatchFlag(f);
}

async function onSendAll() {
  for (const f of flags.value) {
    if (!f.sent && f.instruction.trim()) await dispatchFlag(f);
  }
}
</script>

<template>
  <div class="review-page" data-testid="review-page">
    <header class="rp-header">
      <h2 class="rp-title">Video review</h2>
      <span class="rp-handle">{{ video || "(no video)" }}</span>
    </header>

    <p v-if="loadError" class="rp-error" data-testid="rp-error">
      {{ loadError }}
    </p>

    <div class="rp-cols">
      <!-- Left: player + timeline + flags -->
      <section class="rp-left">
        <video
          v-if="video"
          ref="player"
          class="rp-player"
          data-testid="rp-player"
          controls
          preload="metadata"
          :src="videoUrl()"
          @loadedmetadata="onLoadedMetadata"
        />
        <ChapterTimeline
          :chapters="chapters"
          :total-ms="totalMs"
          :selection="selection"
          @seek="onSeek"
          @select="onSelect"
          @flag="onFlag"
        />
        <FlagList
          :flags="flags"
          :selected-id="selectedId"
          @select="onSelectFlag"
          @send-all="onSendAll"
        />
      </section>

      <!-- Right: selected flag detail -->
      <section class="rp-right">
        <FlagDetail
          :flag="selectedFlag"
          :video="video"
          :chat="selectedFlag ? chats[selectedFlag.id] ?? [] : []"
          :chat-busy="chatBusy"
          @update-instruction="onUpdateInstruction"
          @send-chat="onSendChat"
          @send-refine="onSendRefine"
        />
      </section>
    </div>
  </div>
</template>

<style scoped>
.review-page {
  padding: 1em 1.25em;
  max-width: 1400px;
  margin: 0 auto;
}
.rp-header {
  display: flex;
  align-items: baseline;
  gap: 0.75em;
  margin-bottom: 0.75em;
}
.rp-title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
}
.rp-handle {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  color: #6b7280;
}
.rp-error {
  background: #fff8eb;
  border: 1px solid #f5dca0;
  color: #92590a;
  padding: 0.6em 0.9em;
  border-radius: 6px;
  font-size: 13px;
}
.rp-cols {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 1.5em;
  align-items: start;
}
.rp-left,
.rp-right {
  display: flex;
  flex-direction: column;
  gap: 0.9em;
}
.rp-player {
  width: 100%;
  border-radius: 8px;
  background: #000;
}
@media (max-width: 900px) {
  .rp-cols {
    grid-template-columns: 1fr;
  }
}
</style>
