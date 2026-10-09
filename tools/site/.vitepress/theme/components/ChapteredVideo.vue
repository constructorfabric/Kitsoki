<script setup lang="ts">
/**
 * The demo player: rrweb replay iframe first, with the legacy MP4 + optional
 * chapter sidecar path retained only for explicit fallback media.
 *
 * A rrweb-native story-demo has no mp4 at all (see demo.embed in
 * features/*.yaml) — instead of a black "not rendered" placeholder it embeds
 * the pre-bundled, self-contained Slidey deck html directly, at its scene
 * (`?scene=N`), the same sandboxed-iframe-plus-open-link idiom the in-app
 * `slideshow` media kind already uses (src/components/ViewElement.vue).
 *
 * Degrades by design:
 *  - embedded variant (binary /help/): poster only + "watch online" link —
 *    MP4s and deck embeds are never embedded into the binary.
 *  - replay not staged locally and no embed: poster (or empty placeholder) + a
 *    hint to capture it (`make demo-feature-rrweb FEATURE=<id>`). The site build
 *    never fails on missing media.
 */
import { ref, computed, onMounted } from "vue";
import { withBase, useData } from "vitepress";

interface Media {
  videoUrl: string | null;
  posterUrl: string | null;
  chaptersUrl: string | null;
  videoAvailable: boolean;
  embedKind?: "deck" | "rrweb" | null;
  embedUrl: string | null;
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
const showEmbed = computed(() => !embedded.value && !showVideo.value && !!props.media.embedUrl);
const embedHint = computed(() =>
  props.media.embedKind === "rrweb"
    ? (text.value.rrwebEmbedHint ?? "An interactive rrweb replay — generated directly from the DOM session log, no MP4 render.")
    : (text.value.deckEmbedHint ?? "An interactive clip from the kitsoki story deck — replayed from a real run, no LLM.")
);
const embedTitle = computed(() =>
  props.media.embedKind === "rrweb"
    ? (text.value.rrwebEmbedTitle ?? "interactive replay")
    : (text.value.deckEmbedTitle ?? "interactive deck clip")
);
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
    <!-- rrweb-native story-demo: the pre-bundled, self-contained Slidey deck,
         opened at this feature's scene (`?scene=N`) — the iframe+open-link
         idiom the in-app slideshow media kind uses (ViewElement.vue). Unlike
         the in-app case (agent-produced artifacts, opaque-origin sandbox) this
         deck is OUR committed, reviewed asset served same-origin, and the
         rrweb replayer inside it needs same-origin document access to rebuild
         the recording (an `allow-scripts`-only sandbox leaves it stuck on
         "Loading replay…" forever) — so allow-same-origin, keeping the rest of
         the sandbox (no top-navigation, popups, forms) as defense-in-depth. -->
    <template v-else-if="showEmbed">
      <iframe
        class="kv__embed"
        :src="withBase(media.embedUrl!)"
        sandbox="allow-scripts allow-same-origin"
        :title="`${title} — ${embedTitle}`"
        loading="lazy"
      />
      <p class="kv__badge">
        {{ embedHint }}
        <a :href="withBase(media.embedUrl!)" target="_blank" rel="noopener">{{ text.deckEmbedOpen ?? "Open full-screen" }} →</a>
      </p>
    </template>

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
          {{ text.demoMissing ?? "demo replay not captured in this build" }}
          <code v-if="featureId">make demo-feature-rrweb FEATURE={{ featureId }}</code>
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
