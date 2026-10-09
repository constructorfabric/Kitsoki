<script setup lang="ts">
/**
 * ReviewPage — the /review feedback surface (shipped; see
 * docs/tui/video-review.md). Two columns:
 *
 *   left  — the video player + chapter timeline + flag list
 *   right — the selected flag's still, source_ref, per-flag chat, dispatch
 *
 * It owns the local flag store and the per-flag chat transcripts; the panel
 * captures and dispatches feedback notes (epic shared decision 3) and never
 * edits a spec. Chapters come from runstatus.video.chapters; stills from
 * runstatus.video.frame; dispatch via runstatus.feedback.add; the chat reuses
 * the read-only off-path agent.
 *
 * Route: /review/:sessionId?video=<handle>. The video player + still are media
 * elements resolved through the DataSource.artifactUrl resolver.
 */
import { ref, computed, onMounted, reactive } from "vue";
import { useRoute } from "vue-router";
import { createDataSource } from "../data/source.js";
import type { Chapter, VisualBundle } from "../data/source.js";
import type { Flag } from "../lib/flags.js";
import type { ResolvedElement } from "../lib/resolveElement.js";
import type { AnnotationAnchor } from "../lib/annotationAnchor.js";
import { normalizeAnchor } from "../lib/annotationAnchor.js";
import { dominantChapter } from "../lib/flags.js";
import type { RrwebEvent } from "../data/session-capture.js";
import ChapterTimeline from "../components/ChapterTimeline.vue";
import FlagList from "../components/FlagList.vue";
import FlagDetail from "../components/FlagDetail.vue";
import SpatialPicker from "../components/SpatialPicker.vue";
import ReplayFrame from "../components/ReplayFrame.vue";

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

// Spatial picker over the player frame (docs/tui/spatial-capture.md). The
// frame's natural size feeds the rendered→frame-pixel map; default to a 16:9
// box until the video reports its intrinsic size. In the LIVE-video path the
// resolver runs against the LIVE document (one resolver, two roots); when the
// reviewed media carries recorded rrweb events we instead render a ReplayFrame
// (the reconstructed-DOM root — epic shared decision 2), so a click resolves a
// REAL app control rather than the opaque <video>.
const frameNatural = ref({ width: 1280, height: 720 });
const pickerRoot = computed<Document | null>(() =>
  typeof document !== "undefined" ? document : null
);

// Recorded rrweb session backing this review, when present. `events` ≥ 2
// (Meta + FullSnapshot) means the media is a reconstructed session: render the
// ReplayFrame picker over it. `width`/`height` are the recording's INTRINSIC
// viewport — the replay iframe's own pixel space, which the picker maps clicks
// into (NOT the scaled render size).
const replay = ref<{ events: RrwebEvent[]; width: number; height: number } | null>(
  null
);
const hasReplay = computed(
  () => !!replay.value && replay.value.events.length >= 2
);
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
  // Optional reconstructed-DOM replay: if the source exposes recorded rrweb
  // events for this media, render the ReplayFrame picker over the real UI
  // instead of the opaque <video>. A source without it (snapshot/artifact) omits
  // the method; any failure silently keeps the video path (never blocks review).
  if (ds.videoEvents) {
    try {
      const r = await ds.videoEvents(props.sessionId, video.value);
      if (r.events.length >= 2) {
        replay.value = r;
        if (r.width > 0 && r.height > 0) {
          frameNatural.value = { width: r.width, height: r.height };
        }
      }
    } catch {
      /* no replay sidecar — keep the live-video path */
    }
  }
});

function videoUrl(): string {
  return ds.artifactUrl(video.value);
}

function onLoadedMetadata() {
  if (player.value && !Number.isNaN(player.value.duration)) {
    playerDurationMs.value = Math.round(player.value.duration * 1000);
  }
  if (player.value?.videoWidth && player.value?.videoHeight) {
    frameNatural.value = {
      width: player.value.videoWidth,
      height: player.value.videoHeight,
    };
  }
}

/**
 * The operator clicked/dragged the frame: stash the point + resolved element on
 * the selected flag (epic decision 5 — the flag BECOMES the spatial
 * attachment). The flag's next chat question carries this as the visual bundle.
 */
function onPick(bundle: {
  point: { x: number; y: number };
  box?: { x: number; y: number; width: number; height: number };
  element?: ResolvedElement;
}) {
  const f = selectedFlag.value;
  if (!f) return;
  f.point = bundle.point;
  f.element = bundle.element;
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

/**
 * visualFor builds the off-path `visual` bundle for a flag, or undefined when
 * the flag carries neither a captured still nor a picked point/element (then
 * the question is a plain off-path turn, unchanged from before). The bundle
 * rides on session.offpath; slice 1 lifts it into host.WithVisualAmbient.
 */
function visualFor(f: Flag): VisualBundle | undefined {
  if (!f.frame_handle && !f.point && !f.element) return undefined;
  return {
    ...(f.frame_handle ? { frame_handle: f.frame_handle } : {}),
    ...(video.value ? { media_handle: video.value } : {}),
    ...(f.point ? { point: f.point } : {}),
    ...(f.element ? { element: f.element } : {}),
    t_ms: f.start_ms,
    route: `/review/${props.sessionId}`,
  };
}

/**
 * anchorFor builds the v2 unified AnnotationAnchor for a flag (the generalization
 * of visualFor). The reviewed media is rrweb-backed when `hasReplay`, else a
 * plain mp4; the picked point/element normalizes into a dom_node (or region)
 * target via normalizeAnchor, and the flag's time window rides as the still's
 * t_ms through the back-compat projection. Undefined when the flag carries no
 * spatial pick (then the question is a plain off-path turn).
 */
function anchorFor(f: Flag): AnnotationAnchor | undefined {
  if (!f.point && !f.element && !f.frame_handle) return undefined;
  const meta = {
    media_handle: video.value || undefined,
    media_kind: (hasReplay.value ? "rrweb" : "mp4") as "rrweb" | "mp4",
    frame_handle: f.frame_handle ?? undefined,
    route: `/review/${props.sessionId}`,
  };
  // A picked point/element normalizes into the discriminated target; a flag with
  // only a captured still (no pick) anchors at the time window instead.
  if (f.point || f.element) {
    return normalizeAnchor(
      { point: f.point ?? { x: 0, y: 0 }, element: f.element },
      meta
    );
  }
  return { ...meta, target: { kind: "time_range", start_ms: f.start_ms } };
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
    // Pass both the back-compat visual bundle and the v2 unified anchor; the
    // server lifts whichever it reads into the agent ambient.
    const { answer } = await ds.offpath(
      props.sessionId,
      input,
      visualFor(f),
      anchorFor(f)
    );
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
    // The v2 unified anchor ties this feedback to its exact location; the
    // back-compat time_range/frame_handle stay populated above.
    anchor: anchorFor(f),
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
        <div v-if="video" class="rp-frame" data-testid="rp-frame">
          <!-- Reconstructed-DOM path: when the reviewed media carries recorded
               rrweb events, render the rrweb Replayer (REAL UI) under the picker
               so a click resolves a real app control against the reconstructed
               DOM (epic shared decision 2). The picker lives inside ReplayFrame,
               rooted at the replay iframe's contentDocument. -->
          <ReplayFrame
            v-if="hasReplay && selectedFlag"
            :events="replay!.events"
            :natural-width="frameNatural.width"
            :natural-height="frameNatural.height"
            @pick="onPick"
          />
          <!-- Live-video path (unchanged): the opaque <video> + a transparent
               picker that drops pointer-events to hit-test the page behind it. -->
          <template v-else>
            <video
              ref="player"
              class="rp-player"
              data-testid="rp-player"
              controls
              preload="metadata"
              :src="videoUrl()"
              @loadedmetadata="onLoadedMetadata"
            />
            <!-- Spatial picker: live only once a flag is selected (it stashes the
                 point/element on that flag). pointer-events live so it captures
                 clicks; it drops them momentarily to hit-test the page behind. -->
            <SpatialPicker
              v-if="selectedFlag"
              :natural-width="frameNatural.width"
              :natural-height="frameNatural.height"
              :root="pickerRoot"
              @pick="onPick"
            />
          </template>
        </div>
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
  color: var(--k-fg-muted, #6b7280);
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
.rp-frame {
  position: relative;
}
.rp-player {
  display: block;
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
