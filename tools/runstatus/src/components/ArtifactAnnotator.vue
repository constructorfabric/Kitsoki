<script setup lang="ts">
/**
 * ArtifactAnnotator — the media-kind dispatch surface for unified artifact
 * annotation (.context/unified-artifact-annotation.md). Given a
 * {media_handle, media_kind} it renders the right substrate + annotation layer
 * and emits a single unified AnnotationAnchor regardless of kind:
 *
 *   - png    → <img> + RegionDrawLayer plus optional SemanticOverlay
 *   - mp4    → <video> + timeline + frame-grab, then region/semantic overlay
 *   - rrweb  → ReplayFrame (the spatial-oracle reconstructed-DOM picker:
 *              dom_node / region / semantic_element), normalized via normalizeAnchor
 *   - html   → a static <iframe> + SpatialPicker plus optional SemanticOverlay
 *   - slidey → live embed picks or poster-backed SemanticOverlay
 *
 * This generalizes spatial-oracle: ReplayFrame/SpatialPicker/resolveElement are
 * REUSED for the DOM-bearing kinds; the canvas + semantic overlay cover flat
 * images, HTML/data fields, and poster-backed media. Every path funnels into one `anchor` emit
 * carrying the artifact metadata + a discriminated `target`.
 */
import { ref, computed, onMounted, onBeforeUnmount, watch } from "vue";
import {
  installEmbedPickListener,
  sendAnnotateMode,
} from "../lib/embedView.js";
import type { DataSource } from "../data/source.js";
import type {
  AnnotationAnchor,
  MediaKind,
  PickerBundle,
  RegionShape,
  Box,
  Point,
} from "../lib/annotationAnchor.js";
import type { SemanticElementTarget } from "../lib/annotationAnchor.js";
import { normalizeAnchor, regionToTarget } from "../lib/annotationAnchor.js";
import type { SemanticMap, SemanticSidecar } from "../lib/semanticPlugins.js";
import {
  enrichSemanticMapFromDOM,
  toSemanticMap,
} from "../lib/semanticPlugins.js";
import type { RrwebEvent } from "../data/session-capture.js";
import SpatialPicker from "./SpatialPicker.vue";
import ReplayFrame from "./ReplayFrame.vue";
import RegionDrawLayer from "./RegionDrawLayer.vue";
import SemanticOverlay from "./SemanticOverlay.vue";

const props = defineProps<{
  /** The DataSource (DI) — resolves artifact URLs, sidecars, frame grabs. */
  ds: DataSource;
  /** The session the annotation belongs to (for sidecar / frame RPCs). */
  sessionId: string;
  /** The artifact handle being annotated. */
  mediaHandle: string;
  /** Which media kind to render. */
  mediaKind: MediaKind;
  /**
   * Optional still-image backdrop handle for the `slidey` path. A slideshow is
   * emitted as an mp4 (or html) whose pixels aren't an addressable still, so a
   * sidecar-bearing deck floats its SemanticOverlay over a poster/frame image of
   * the producer's frame instead of an <iframe>/<video>. Defaults to
   * `mediaHandle` (correct when the media itself is an image).
   */
  posterHandle?: string;
  /** Use the producer's live embedded picker instead of sidecar/poster overlay. */
  liveEmbed?: boolean;
  /** Producer-native view state to restore before enabling live annotation. */
  embedScope?: string;
  embedStep?: string;
  /** The route the capture happens on (rides on the emitted anchor). */
  route?: string;
  /** Optional recorded rrweb events for the rrweb kind (else fetched lazily). */
  events?: RrwebEvent[];
  /** The rrweb recording's intrinsic viewport, when known. */
  naturalWidth?: number;
  naturalHeight?: number;
}>();

const emit = defineEmits<{
  (e: "anchor", anchor: AnnotationAnchor): void;
}>();

// ── Shared metadata woven onto every emitted anchor ──────────────────────────
function meta(extra: Partial<AnnotationAnchor> = {}): Omit<AnnotationAnchor, "target"> {
  return {
    media_handle: props.mediaHandle,
    media_kind: props.mediaKind,
    ...(props.route ? { route: props.route } : {}),
    ...extra,
  };
}

const naturalSize = ref({
  width: props.naturalWidth ?? 1280,
  height: props.naturalHeight ?? 720,
});

// ── png / frame still: the region draw tool ──────────────────────────────────
const regionShape = ref<RegionShape>("box");

function imgUrl(handle: string): string {
  return props.ds.artifactUrl(handle);
}

function onImgLoad(ev: Event): void {
  const img = ev.target as HTMLImageElement;
  if (img.naturalWidth && img.naturalHeight) {
    naturalSize.value = { width: img.naturalWidth, height: img.naturalHeight };
  }
}

/** The still currently under the region tool (a png is itself; an mp4 grab is a
 *  captured frame handle). Null until an mp4 frame is grabbed. */
const stillHandle = ref<string | null>(
  props.mediaKind === "png" ? props.mediaHandle : null
);

function onRegion(region: { shape: RegionShape; bbox: Box; path?: Point[] }): void {
  emit("anchor", {
    ...meta(stillHandle.value ? { frame_handle: stillHandle.value } : {}),
    target: regionToTarget(region),
  });
}

// ── mp4: a <video> + timeline + frame-grab, then the region tool ─────────────
const video = ref<HTMLVideoElement | null>(null);
const tMs = ref(0);
const grabbing = ref(false);

function onVideoMeta(): void {
  const v = video.value;
  if (v?.videoWidth && v?.videoHeight) {
    naturalSize.value = { width: v.videoWidth, height: v.videoHeight };
  }
}

function onScrub(ev: Event): void {
  const v = video.value;
  const ms = Number((ev.target as HTMLInputElement).value);
  tMs.value = ms;
  if (v) v.currentTime = ms / 1000;
}

const durationMs = computed(() =>
  video.value && !Number.isNaN(video.value.duration)
    ? Math.round(video.value.duration * 1000)
    : 0
);

/** Emit a time_range anchor for the current scrub position (no still). */
function markTime(): void {
  emit("anchor", { ...meta(), target: { kind: "time_range", start_ms: tMs.value } });
}

/** Grab a still at the current time, then switch to the region tool over it. */
async function grabFrame(): Promise<void> {
  grabbing.value = true;
  try {
    const { handle } = await props.ds.videoFrame(
      props.sessionId,
      props.mediaHandle,
      tMs.value
    );
    stillHandle.value = handle;
  } finally {
    grabbing.value = false;
  }
}

// ── rrweb: the reconstructed-DOM picker (ReplayFrame), normalized ────────────
const replayEvents = ref<RrwebEvent[]>(props.events ?? []);
const hasReplay = computed(() => replayEvents.value.length >= 2);

async function loadReplay(): Promise<void> {
  if (props.events && props.events.length >= 2) return;
  if (!props.ds.videoEvents) return;
  try {
    const r = await props.ds.videoEvents(props.sessionId, props.mediaHandle);
    if (r.events.length >= 2) {
      replayEvents.value = r.events;
      if (r.width > 0 && r.height > 0) {
        naturalSize.value = { width: r.width, height: r.height };
      }
    }
  } catch {
    /* no replay sidecar — the rrweb path renders its "unavailable" state */
  }
}

/** ReplayFrame / SpatialPicker emit the legacy flat bundle; normalize it into
 *  the discriminated anchor (dom_node when an element resolved, else region). */
function onPickerBundle(bundle: PickerBundle): void {
  emit("anchor", normalizeAnchor(bundle, meta()));
}

// ── html: a static iframe + the live-DOM SpatialPicker ───────────────────────
const iframe = ref<HTMLIFrameElement | null>(null);
const iframeRoot = ref<Document | null>(null);

function onIframeLoad(): void {
  const doc = iframe.value?.contentDocument ?? null;
  iframeRoot.value = doc;
  const win = doc?.defaultView;
  const width = Math.round(win?.innerWidth || doc?.documentElement?.clientWidth || 0);
  const height = Math.round(win?.innerHeight || doc?.documentElement?.clientHeight || 0);
  if (width > 0 && height > 0) {
    naturalSize.value = { width, height };
  }
}

// ── slidey: a still backdrop + the semantic overlay (sidecar-driven) ─────────
/** The raw sidecar envelope (null until fetched / when none exists). Kept raw
 *  so the overlay map is derived REACTIVELY against `naturalSize`: the boxes are
 *  in the producer's natural pixels, and the backdrop poster's load reports the
 *  true pixel space, so the markers reposition once the still measures itself. */
const semanticSidecar = ref<SemanticSidecar | null>(null);
const semanticError = ref<string | null>(null);

/** The overlay map, recomputed whenever the sidecar or the measured natural
 *  size changes (the poster <img> sets naturalSize on load). */
const semanticMap = computed<SemanticMap | null>(() =>
  semanticSidecar.value
    ? enrichSemanticMapFromDOM(
        toSemanticMap(semanticSidecar.value, props.mediaHandle, naturalSize.value),
        props.mediaKind === "html" ? iframeRoot.value : null
      )
    : null
);

/** The backdrop still URL for the slidey overlay: a poster/frame image of the
 *  deck. A slideshow's base artifact (an mp4) isn't an addressable still, so the
 *  caller passes a poster handle and we resolve its sibling poster URL
 *  (`/artifact/<handle>/poster`). When the media IS itself an image (no distinct
 *  poster handle), the media's own URL is the backdrop. */
const slideyBackdropUrl = computed(() => {
  const posterHandle = props.posterHandle ?? props.mediaHandle;
  // The media itself is the still (a png deck): use its plain artifact URL.
  if (posterHandle === props.mediaHandle && props.mediaKind === "png") {
    return imgUrl(posterHandle);
  }
  // A video/slideshow-backed deck: resolve the sibling poster still, falling
  // back to the plain artifact URL when the source has no poster convention.
  return props.ds.artifactPosterUrl
    ? props.ds.artifactPosterUrl(posterHandle)
    : imgUrl(posterHandle);
});

async function loadSemantic(): Promise<void> {
  if (!props.ds.semanticMap) return;
  try {
    const env = await props.ds.semanticMap(props.sessionId, props.mediaHandle);
    if (env && env.elements.length > 0) {
      semanticSidecar.value = env;
    }
  } catch (e) {
    // No sidecar ⇒ fall back to the still + SpatialPicker (dom_node) path.
    semanticError.value = e instanceof Error ? e.message : String(e);
  }
}

function onSemanticPick(target: SemanticElementTarget): void {
  emit("anchor", { ...meta(), target });
}

function boxFromTuple(b: [number, number, number, number]): Box {
  return { x: b[0], y: b[1], width: b[2], height: b[3] };
}

// ── slidey (live embed) — element picking ON the interactive deck ─────────────
// The flagship path: rather than a static poster + sidecar overlay, the live deck
// owns spatial feedback. We turn on annotation mode in the embedded deck and
// receive a precise `embed:pick` ({scope, ref, bbox}) when the operator points at
// a real element on the slide. The producer is opaque — kitsoki round-trips the
// ref into the anchor without interpreting it.
const embedFrame = ref<HTMLIFrameElement | null>(null);
let _teardownPick: (() => void) | null = null;

/** The live-embed substrate is used for a `slidey` deck — a multi-scene producer
 *  that speaks the embed protocol on its own live surface. The poster/overlay and
 *  iframe+picker branches remain for non-embed slidey-mapped artifacts. */
const useLiveEmbed = computed<boolean>(() => props.mediaKind === "slidey" && props.liveEmbed === true);

function onEmbedLoad(): void {
  // Ask the deck to enter annotation mode once it has booted.
  sendAnnotateMode(embedFrame.value?.contentWindow ?? null, true, {
    ...(props.embedScope ? { scope: props.embedScope } : {}),
    ...(props.embedStep ? { step: props.embedStep } : {}),
  });
}

onMounted(() => {
  if (props.mediaKind === "rrweb") void loadReplay();
  if (!useLiveEmbed.value) void loadSemantic();
  if (useLiveEmbed.value) {
    _teardownPick = installEmbedPickListener((pick) => {
      // Build the discriminated semantic_element anchor the refine consumes; the
      // ref ("<scene>/<field>") + bbox come straight from the producer.
      emit("anchor", {
        ...meta(),
        target: {
          kind: "semantic_element",
          plugin: pick.producer ?? "embed",
          ref: pick.ref,
          label: pick.label ?? pick.ref,
          ...(pick.bbox ? { bbox: boxFromTuple(pick.bbox) } : {}),
        } as SemanticElementTarget,
      });
    });
  }
});

onBeforeUnmount(() => {
  // Turn annotation mode back off in the deck and drop the listener.
  if (useLiveEmbed.value) sendAnnotateMode(embedFrame.value?.contentWindow ?? null, false);
  _teardownPick?.();
  _teardownPick = null;
});
watch(
  () => [props.mediaHandle, props.mediaKind],
  () => {
    semanticSidecar.value = null;
    stillHandle.value = props.mediaKind === "png" ? props.mediaHandle : null;
    if (props.mediaKind === "rrweb") void loadReplay();
    if (!useLiveEmbed.value) void loadSemantic();
  }
);
</script>

<template>
  <div class="artifact-annotator" data-testid="artifact-annotator">
    <!-- png: image + region draw -->
    <div v-if="mediaKind === 'png'" class="aa-stage" data-testid="aa-png">
      <img
        class="aa-media"
        :src="imgUrl(mediaHandle)"
        alt="annotated artifact"
        @load="onImgLoad"
      />
      <RegionDrawLayer
        :natural-width="naturalSize.width"
        :natural-height="naturalSize.height"
        :shape="regionShape"
        @region="onRegion"
      />
      <SemanticOverlay v-if="semanticMap" :map="semanticMap" @pick="onSemanticPick" />
      <div class="aa-tools">
        <button
          v-for="s in (['box', 'highlight', 'freeform'] as RegionShape[])"
          :key="s"
          type="button"
          class="aa-tool"
          :class="{ 'aa-tool--on': regionShape === s }"
          :data-testid="`aa-tool-${s}`"
          @click="regionShape = s"
        >
          {{ s }}
        </button>
      </div>
    </div>

    <!-- mp4: video + scrub + grab-frame → region draw on the grabbed still -->
    <div v-else-if="mediaKind === 'mp4'" class="aa-stage" data-testid="aa-mp4">
      <template v-if="stillHandle">
        <img class="aa-media" :src="imgUrl(stillHandle)" alt="grabbed frame" @load="onImgLoad" />
        <RegionDrawLayer
          :natural-width="naturalSize.width"
          :natural-height="naturalSize.height"
          :shape="regionShape"
          @region="onRegion"
        />
        <SemanticOverlay v-if="semanticMap" :map="semanticMap" @pick="onSemanticPick" />
        <div class="aa-tools">
          <button
            v-for="s in (['box', 'highlight', 'freeform'] as RegionShape[])"
            :key="s"
            type="button"
            class="aa-tool"
            :class="{ 'aa-tool--on': regionShape === s }"
            :data-testid="`aa-tool-${s}`"
            @click="regionShape = s"
          >
            {{ s }}
          </button>
          <button type="button" class="aa-tool" data-testid="aa-regrab" @click="stillHandle = null">
            re-grab
          </button>
        </div>
      </template>
      <template v-else>
        <video
          ref="video"
          class="aa-media"
          data-testid="aa-video"
          preload="metadata"
          :src="imgUrl(mediaHandle)"
          @loadedmetadata="onVideoMeta"
        />
        <div class="aa-tools">
          <input
            type="range"
            class="aa-scrub"
            data-testid="aa-scrub"
            min="0"
            :max="durationMs"
            :value="tMs"
            @input="onScrub"
          />
          <button type="button" class="aa-tool" data-testid="aa-mark-time" @click="markTime">
            mark time
          </button>
          <button
            type="button"
            class="aa-tool"
            data-testid="aa-grab"
            :disabled="grabbing"
            @click="grabFrame"
          >
            {{ grabbing ? "grabbing…" : "grab frame" }}
          </button>
        </div>
      </template>
    </div>

    <!-- rrweb: the spatial-oracle reconstructed-DOM picker, normalized -->
    <div v-else-if="mediaKind === 'rrweb'" class="aa-stage" data-testid="aa-rrweb">
      <ReplayFrame
        v-if="hasReplay"
        :events="replayEvents"
        :natural-width="naturalSize.width"
        :natural-height="naturalSize.height"
        :semantic-map="semanticMap"
        @pick="onPickerBundle"
        @semantic-pick="onSemanticPick"
      />
      <p v-else class="aa-muted" data-testid="aa-rrweb-empty">No session replay available.</p>
    </div>

    <!-- html: a static iframe + the live-DOM picker (dom_node) -->
    <div v-else-if="mediaKind === 'html'" class="aa-stage" data-testid="aa-html">
      <iframe
        ref="iframe"
        class="aa-media aa-iframe"
        data-testid="aa-iframe"
        :src="imgUrl(mediaHandle)"
        @load="onIframeLoad"
      />
      <SpatialPicker
        :natural-width="naturalSize.width"
        :natural-height="naturalSize.height"
        :root="iframeRoot"
        @pick="onPickerBundle"
      />
      <SemanticOverlay v-if="semanticMap" :map="semanticMap" @pick="onSemanticPick" />
    </div>

    <!-- slidey: a still poster backdrop + the semantic overlay (sidecar). A
         slidey deck is a multi-scene render (mp4 / pdf / html) whose pixels
         aren't an addressable still, so the overlay floats over a poster image
         sized to the producer's natural frame; the markers are positioned as a
         percent of that natural space (so they track the still at any CSS
         scale). When no sidecar resolves, fall back to the iframe + live-DOM
         picker (dom_node). -->
    <div v-else-if="mediaKind === 'slidey'" class="aa-stage" data-testid="aa-slidey">
      <!-- live embed: the interactive deck owns spatial feedback. We turn on its
           annotation mode and receive a precise embed:pick when the operator
           points at a real element on the slide. allow-scripts lets the deck
           boot; it posts picks to the parent via postMessage. -->
      <template v-if="useLiveEmbed">
        <iframe
          ref="embedFrame"
          class="aa-media aa-iframe"
          data-testid="aa-slidey-embed"
          sandbox="allow-scripts"
          :src="imgUrl(mediaHandle)"
          @load="onEmbedLoad"
        />
        <p class="aa-hint" data-testid="aa-slidey-hint">
          Point at an element on the slide to refine it.
        </p>
      </template>
      <template v-else-if="semanticMap">
        <img
          class="aa-media"
          data-testid="aa-slidey-poster"
          :src="slideyBackdropUrl"
          alt="deck frame"
          @load="onImgLoad"
        />
        <SemanticOverlay :map="semanticMap" @pick="onSemanticPick" />
      </template>
      <template v-else>
        <iframe
          ref="iframe"
          class="aa-media aa-iframe"
          data-testid="aa-slidey-iframe"
          :src="imgUrl(mediaHandle)"
          @load="onIframeLoad"
        />
        <SpatialPicker
          :natural-width="naturalSize.width"
          :natural-height="naturalSize.height"
          :root="iframeRoot"
          @pick="onPickerBundle"
        />
      </template>
    </div>
  </div>
</template>

<style scoped>
.artifact-annotator {
  position: relative;
}
.aa-stage {
  position: relative;
  width: 100%;
  background: #06101b;
  border-radius: 8px;
  overflow: hidden;
}
.aa-media {
  display: block;
  width: 100%;
  height: auto;
}
.aa-iframe {
  border: none;
  background: #fff;
  min-height: 320px;
  aspect-ratio: 16 / 9;
}
.aa-tools {
  position: absolute;
  left: 8px;
  bottom: 8px;
  display: flex;
  gap: 6px;
  align-items: center;
  z-index: 3;
}
.aa-tool {
  font-size: 11px;
  padding: 3px 8px;
  border-radius: 5px;
  border: 1px solid rgba(148, 163, 184, 0.5);
  background: rgba(2, 6, 23, 0.7);
  color: #e2e8f0;
  cursor: pointer;
}
.aa-tool--on {
  border-color: #fbbf24;
  color: #fbbf24;
}
.aa-tool:disabled {
  opacity: 0.5;
  cursor: default;
}
.aa-scrub {
  width: 160px;
}
.aa-muted {
  color: #64748b;
  font-size: 0.78rem;
  padding: 1rem;
  margin: 0;
}
.aa-hint {
  position: absolute;
  left: 8px;
  bottom: 8px;
  z-index: 3;
  margin: 0;
  padding: 4px 10px;
  font-size: 11px;
  color: #e2e8f0;
  background: rgba(2, 6, 23, 0.72);
  border: 1px solid rgba(56, 189, 248, 0.6);
  border-radius: 6px;
  pointer-events: none;
}
</style>
