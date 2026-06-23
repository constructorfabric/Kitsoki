<script setup lang="ts">
/**
 * The demo-video player: MP4 + optional chapter sidecar (the recording specs'
 * <video>.mp4.chapters.json) rendered as a clickable chapter rail that seeks
 * the video and highlights the active chapter during playback.
 *
 * Degrades by design:
 *  - embedded variant (binary /help/): poster only + "watch online" link —
 *    MP4s are never embedded into the binary.
 *  - video not staged locally: poster (or empty placeholder) + a hint to
 *    record it (`make demo-feature FEATURE=<id>`). The site build never fails
 *    on missing media.
 */
import { ref, computed, onMounted } from "vue";
import { withBase, useData } from "vitepress";

interface Media {
  videoUrl: string | null;
  posterUrl: string | null;
  chaptersUrl: string | null;
  videoAvailable: boolean;
}

interface Chapter {
  index: number;
  id: string;
  label: string;
  start_ms: number;
  end_ms: number;
  /** Set on a stitched master sidecar: the section this chapter belongs to. */
  group?: string;
  group_label?: string;
  /** The section's title card — rendered as the group header, not a button. */
  intro?: boolean;
}

interface ChapterGroup {
  key: string;
  label: string;
  chapters: Chapter[];
}

const props = defineProps<{
  media: Media;
  title: string;
  featureId?: string;
}>();

const { theme } = useData();
const embedded = computed(() => theme.value.siteVariant === "embedded");
const text = computed(() => theme.value.siteText?.labels ?? {});
const showVideo = computed(() => !embedded.value && props.media.videoAvailable);
const watchOnlineUrl = computed(() =>
  props.featureId
    ? `${theme.value.sitePublicUrl}${theme.value.siteLocale === "en" ? "" : `/${theme.value.siteLocale}`}/features/${props.featureId}.html`
    : theme.value.sitePublicUrl,
);

const videoEl = ref<HTMLVideoElement | null>(null);
const chapters = ref<Chapter[]>([]);
const activeIdx = ref(-1);

/** Consecutive section groups for a stitched master's rail (its chapters carry a
 *  `group`); null for a plain per-feature sidecar, which renders flat. */
const groups = computed<ChapterGroup[] | null>(() => {
  const cs = chapters.value;
  if (!cs.some((c) => c.group)) return null;
  const out: ChapterGroup[] = [];
  for (const c of cs) {
    const key = c.group ?? "";
    const last = out[out.length - 1];
    if (last && last.key === key) last.chapters.push(c);
    else out.push({ key, label: c.group_label ?? c.label, chapters: [c] });
  }
  return out;
});
const activeGroup = computed(() => chapters.value[activeIdx.value]?.group ?? null);

onMounted(async () => {
  if (!showVideo.value || !props.media.chaptersUrl) return;
  try {
    const res = await fetch(withBase(props.media.chaptersUrl));
    if (res.ok) chapters.value = await res.json();
  } catch {
    /* chapters are an enhancement — the plain player stands alone */
  }
});

function onTimeUpdate() {
  const v = videoEl.value;
  if (!v || chapters.value.length === 0) return;
  const ms = v.currentTime * 1000;
  activeIdx.value = chapters.value.findIndex((c) => ms >= c.start_ms && ms < c.end_ms);
}

function seek(c: Chapter) {
  const v = videoEl.value;
  if (!v) return;
  v.currentTime = c.start_ms / 1000;
  void v.play();
}

/** Step-card click-through: seek to the chapter recorded for a tour step id. */
function seekToStep(stepId: string) {
  const c = chapters.value.find((ch) => ch.id === stepId);
  if (c) seek(c);
}

defineExpose({ seekToStep, hasChapters: () => chapters.value.length > 0 });
</script>

<template>
  <figure class="kv">
    <video
      v-if="showVideo"
      ref="videoEl"
      class="kv__video"
      controls
      preload="metadata"
      :poster="media.posterUrl ? withBase(media.posterUrl) : undefined"
      :src="withBase(media.videoUrl!)"
      @timeupdate="onTimeUpdate"
    />
    <div v-else class="kv__placeholder">
      <img
        v-if="media.posterUrl"
        class="kv__poster"
        :src="withBase(media.posterUrl)"
        :alt="`${title} — ${text.demoPosterAlt ?? 'demo poster frame'}`"
        loading="lazy"
      />
      <div v-else class="kv__poster kv__poster--empty" aria-hidden="true">▶</div>
      <p class="kv__badge">
        <template v-if="embedded">
          <a :href="watchOnlineUrl" target="_blank" rel="noopener">{{ text.watchOnline ?? "Watch this demo online" }} →</a>
        </template>
        <template v-else>
          {{ text.demoMissing ?? "demo video not rendered in this build" }}
          <code v-if="featureId">make demo-feature FEATURE={{ featureId }}</code>
        </template>
      </p>
    </div>

    <nav v-if="showVideo && groups" class="kv__chapters kv__chapters--grouped" :aria-label="text.videoChapters ?? 'Video chapters'">
      <div v-for="g in groups" :key="g.key" class="kv__group">
        <button
          class="kv__group-head"
          :class="{ 'kv__group-head--active': activeGroup === g.key }"
          type="button"
          @click="seek(g.chapters[0])"
        >
          {{ g.label }}
        </button>
        <div class="kv__group-body">
          <button
            v-for="c in g.chapters.filter((ch) => !ch.intro)"
            :key="c.id"
            class="kv__chapter"
            :class="{ 'kv__chapter--active': chapters[activeIdx]?.id === c.id }"
            type="button"
            @click="seek(c)"
          >
            {{ c.label }}
          </button>
        </div>
      </div>
    </nav>

    <nav v-else-if="showVideo && chapters.length" class="kv__chapters" :aria-label="text.videoChapters ?? 'Video chapters'">
      <button
        v-for="c in chapters"
        :key="c.id"
        class="kv__chapter"
        :class="{ 'kv__chapter--active': chapters[activeIdx]?.id === c.id }"
        type="button"
        @click="seek(c)"
      >
        {{ c.label }}
      </button>
    </nav>
  </figure>
</template>
