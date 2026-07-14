<script setup lang="ts">
/**
 * ReplayFrame — an rrweb Replayer rendered as a still frame with a SpatialPicker
 * overlaid, so a click resolves a REAL app element against the reconstructed DOM
 * (epic shared decision 2: "rrweb's reconstructed DOM is the pixel↔element
 * bridge"). This is the "second root" half of "one resolver, two roots"
 * (lib/resolveElement): the live-document path resolves the page behind a
 * transparent overlay, this path resolves the Replayer iframe's contentDocument.
 *
 * It reuses BugReportModal.vue's Replayer setup verbatim (lazy import('rrweb'),
 * `new Replayer(events, { root })`, pause on the last frame so a populated frame
 * shows). The difference is the picker: we do NOT scale via the live page rect
 * blindly — the recording was captured at a FIXED intrinsic viewport
 * (`natural-width`/`natural-height`, e.g. 1280×720), which is the iframe's own
 * coordinate space. SpatialPicker maps the operator's click from the OVERLAY's
 * rendered rect into those natural pixels and calls
 * `elementFromPoint(contentDocument, framePt.x, framePt.y)` — so the overlay
 * MUST cover exactly the rendered (CSS-scaled) iframe, and natural-width/height
 * MUST be the recording's intrinsic size, never the scaled offset size.
 *
 * The frame is paused (static), not played — this surface is for pointing at a
 * reconstructed state, not scrubbing (that's BugReportModal's job). It emits the
 * same `{ point, box?, element }` bundle SpatialPicker emits.
 */
import { ref, watch, onMounted, onBeforeUnmount, computed } from "vue";
import SpatialPicker from "./SpatialPicker.vue";
import SemanticOverlay from "./SemanticOverlay.vue";
import type { ResolvedElement } from "../lib/resolveElement.js";
import type { SemanticMap } from "../lib/semanticPlugins.js";
import { enrichSemanticMapFromDOM } from "../lib/semanticPlugins.js";
import type { SemanticElementTarget } from "../lib/annotationAnchor.js";
import type { RrwebEvent } from "../data/session-capture.js";

const props = defineProps<{
  /** The rrweb events array to reconstruct (Meta + FullSnapshot at minimum). */
  events: RrwebEvent[];
  /** The recording's intrinsic viewport width — the iframe's own pixel space. */
  naturalWidth: number;
  /** The recording's intrinsic viewport height. */
  naturalHeight: number;
  /** Optional producer-declared semantic fields to overlay on the replay DOM. */
  semanticMap?: SemanticMap | null;
}>();

const emit = defineEmits<{
  (
    e: "pick",
    bundle: {
      point: { x: number; y: number };
      box?: { x: number; y: number; width: number; height: number };
      element?: ResolvedElement;
    }
  ): void;
  (e: "semantic-pick", target: SemanticElementTarget): void;
}>();

/** Minimal shape of rrweb's Replayer (same subset BugReportModal.vue drives). */
interface RrwebReplayer {
  wrapper: HTMLElement;
  iframe: HTMLIFrameElement;
  pause(timeOffset?: number): void;
  getMetaData(): { startTime: number; endTime: number; totalTime: number };
  destroy(): void;
}

const host = ref<HTMLElement | null>(null);
let player: RrwebReplayer | null = null;

// The replay iframe's contentDocument — the picker's resolve root. Null until the
// Replayer has built its iframe; the picker still emits point/box without it.
const replayRoot = ref<Document | null>(null);
const ready = ref(false);
const failed = ref(false);

// The rendered (CSS-scaled) size of the iframe, so the overlay covers it exactly.
// The picker maps clicks from THIS rect into the natural (intrinsic) pixels.
const renderW = ref(0);
const renderH = ref(0);

const aspectStyle = computed(() => ({
  // Reserve the box at the recording's aspect ratio so layout is stable before
  // the iframe sizes (and the overlay has somewhere to live).
  aspectRatio: `${props.naturalWidth} / ${props.naturalHeight}`,
}));

const replaySemanticMap = computed<SemanticMap | null>(() =>
  props.semanticMap
    ? enrichSemanticMapFromDOM(props.semanticMap, replayRoot.value)
    : null
);

async function mountPlayer(): Promise<void> {
  ready.value = false;
  failed.value = false;
  replayRoot.value = null;
  if (!host.value || props.events.length < 2) {
    failed.value = true;
    return;
  }
  try {
    const mod = await import("rrweb");
    await import("rrweb/dist/style.css");
    const Replayer = (mod as { Replayer?: unknown }).Replayer as
      | (new (e: unknown[], c: Record<string, unknown>) => RrwebReplayer)
      | undefined;
    if (!Replayer) {
      failed.value = true;
      return;
    }
    player = new Replayer(props.events as unknown[], {
      root: host.value,
      speed: 1,
      skipInactive: true,
      showWarning: false,
      mouseTail: false,
    });
    const meta = player.getMetaData();
    // Pause on the last frame — the richest reconstructed state (mirrors
    // BugReportModal). `|| 1` so a zero-duration snapshot still renders.
    player.pause(meta.totalTime || 1);
    // Wait a tick for rrweb to build the iframe DOM, then size + scale.
    await new Promise((r) => requestAnimationFrame(() => r(null)));
    scaleReplayToFit();
    await refreshReplayRoot();
    ready.value = true;
  } catch {
    player = null;
    failed.value = true;
  }
}

/**
 * scaleReplayToFit CSS-scales the Replayer's wrapper so the intrinsic
 * naturalWidth×naturalHeight iframe fits the host width, and records the
 * resulting rendered size so the overlay covers exactly the scaled frame. The
 * picker's natural-width/height stays the INTRINSIC size — the scale folds into
 * the overlay's rendered rect (SpatialPicker.toFramePoint).
 */
function scaleReplayToFit(): void {
  const h = host.value;
  const wrapper = player?.wrapper;
  if (!h || !wrapper) return;
  const scale = h.clientWidth > 0 ? h.clientWidth / props.naturalWidth : 1;
  wrapper.style.transformOrigin = "top left";
  wrapper.style.transform = `scale(${scale})`;
  renderW.value = props.naturalWidth * scale;
  renderH.value = props.naturalHeight * scale;
}

function destroyPlayer(): void {
  try {
    player?.destroy();
  } catch {
    /* ignore */
  }
  player = null;
  replayRoot.value = null;
}

async function refreshReplayRoot(): Promise<void> {
  const doc = player?.iframe.contentDocument ?? null;
  if (!doc) {
    replayRoot.value = null;
    return;
  }
  await waitForReplaySelectors(doc);
  // Force Vue to recompute selector-enriched semantic maps even when the same
  // iframe Document object is reused after rrweb finishes applying the snapshot.
  replayRoot.value = null;
  await Promise.resolve();
  replayRoot.value = doc;
}

async function waitForReplaySelectors(doc: Document): Promise<void> {
  const selectors = (props.semanticMap?.elements ?? [])
    .map((el) => el.selector)
    .filter((selector): selector is string => Boolean(selector));
  if (selectors.length === 0) return;
  for (let i = 0; i < 20; i++) {
    if (selectors.some((selector) => selectorMatches(doc, selector))) return;
    await new Promise((r) => setTimeout(r, 50));
  }
}

function selectorMatches(doc: Document, selector: string): boolean {
  try {
    return Boolean(doc.querySelector(selector));
  } catch {
    return false;
  }
}

onMounted(mountPlayer);
onBeforeUnmount(destroyPlayer);

// Re-mount if the events array is swapped (e.g. a different reviewed session).
watch(
  () => props.events,
  async () => {
    destroyPlayer();
    await Promise.resolve();
    await mountPlayer();
  }
);

watch(
  () => props.semanticMap,
  async () => {
    if (!ready.value) return;
    await refreshReplayRoot();
  }
);
</script>

<template>
  <div
    ref="host"
    class="replay-frame"
    data-testid="rp-replay-frame"
    :style="aspectStyle"
  >
    <p v-if="failed" class="rf-muted" data-testid="rp-replay-error">
      No session replay available.
    </p>
    <!-- The picker overlays the rendered (scaled) iframe exactly. Its
         natural-width/height is the recording's INTRINSIC viewport, so a click
         maps back into the iframe's own pixels for elementFromPoint. -->
    <SpatialPicker
      v-if="ready"
      class="rf-picker"
      :style="{ width: renderW + 'px', height: renderH + 'px' }"
      :natural-width="naturalWidth"
      :natural-height="naturalHeight"
      :root="replayRoot"
      @pick="(b) => emit('pick', b)"
    />
    <SemanticOverlay
      v-if="ready && replaySemanticMap"
      class="rf-semantic"
      :style="{ width: renderW + 'px', height: renderH + 'px' }"
      :map="replaySemanticMap"
      @pick="(target) => emit('semantic-pick', target)"
    />
  </div>
</template>

<style scoped>
.replay-frame {
  position: relative;
  width: 100%;
  overflow: hidden;
  border-radius: 8px;
  background: #06101b;
}
/* rrweb injects a .replayer-wrapper holding the iframe; pin it to the top-left
   so our `scale(...) transform-origin: top left` aligns the rendered frame with
   the overlay (which we also pin top-left at the scaled size). */
.replay-frame :deep(.replayer-wrapper) {
  position: absolute;
  top: 0;
  left: 0;
}
.replay-frame :deep(iframe) {
  border: none;
  background: #fff;
}
/* The overlay is pinned top-left and sized to the rendered (scaled) frame so
   SpatialPicker's rect→natural map is exact. It sits ABOVE the rrweb wrapper
   (which is position:absolute) so it — not the replay iframe — receives clicks;
   the picker then hit-tests the iframe's contentDocument directly (it never
   needs to "see through" to the iframe via the page, unlike the live-DOM path). */
.rf-picker {
  position: absolute;
  top: 0;
  left: 0;
  z-index: 2;
}
.rf-semantic {
  position: absolute;
  top: 0;
  left: 0;
  z-index: 3;
}
.rf-muted {
  color: #64748b;
  font-size: 0.78rem;
  padding: 1rem;
  margin: 0;
}
</style>
