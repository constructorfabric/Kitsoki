<script setup lang="ts">
/**
 * StoryViewer — a SELF-CONTAINED, reusable read-only renderer for a room's
 * typed view plus an optional world snapshot. It takes its data entirely
 * through props (no Pinia store, no DataSource, no session): the same component
 * can render a static editor preview, a cassette-replay snapshot, or anything
 * else with a View + world map.
 *
 * Two layout modes:
 *   - "column" (default): inline 50%-width panel inside the workbench.
 *   - "modal": a centred overlay; emits `close` for the backdrop / close button.
 */
import { computed } from "vue";
import ViewElement from "../ViewElement.vue";
import type { View } from "../../data/editor.js";

const props = withDefaults(
  defineProps<{
    view?: View | null;
    /** Post-bind / current world values keyed by world var name. */
    worldSnapshot?: Record<string, unknown> | null;
    mode?: "column" | "modal";
    title?: string;
  }>(),
  { mode: "column", title: "Story view" }
);

const emit = defineEmits<{ (e: "close"): void }>();

const elements = computed(() => props.view?.Elements ?? []);
const worldEntries = computed<[string, unknown][]>(() =>
  Object.entries(props.worldSnapshot ?? {})
);

function fmt(v: unknown): string {
  if (v === null || v === undefined) return "";
  if (typeof v === "string") return v;
  return JSON.stringify(v, null, 2);
}
</script>

<template>
  <div
    class="story-viewer"
    :class="`story-viewer--${mode}`"
    data-testid="editor-story-viewer"
  >
    <div class="story-viewer__backdrop" v-if="mode === 'modal'" @click="emit('close')" />
    <div class="story-viewer__panel">
      <div class="story-viewer__head">
        <span class="story-viewer__title">{{ title }}</span>
        <button
          v-if="mode === 'modal'"
          class="story-viewer__close"
          data-testid="editor-story-viewer-close"
          @click="emit('close')"
        >✕</button>
      </div>

      <div class="story-viewer__body">
        <section class="story-viewer__view" data-testid="editor-story-viewer-view">
          <p v-if="elements.length === 0" class="story-viewer__empty">
            This room declares no typed view.
          </p>
          <ViewElement
            v-for="(el, i) in elements"
            :key="i"
            :element="el"
          />
        </section>

        <section
          v-if="worldEntries.length > 0"
          class="story-viewer__world"
          data-testid="editor-story-viewer-world"
        >
          <h4 class="story-viewer__world-title">World snapshot</h4>
          <dl class="story-viewer__world-list">
            <template v-for="[k, v] in worldEntries" :key="k">
              <dt>{{ k }}</dt>
              <dd><pre>{{ fmt(v) }}</pre></dd>
            </template>
          </dl>
        </section>
      </div>
    </div>
  </div>
</template>

<style scoped>
.story-viewer--column {
  width: 100%;
}
.story-viewer--modal {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
}
.story-viewer__backdrop {
  position: absolute;
  inset: 0;
  background: rgba(0, 0, 0, 0.55);
}
.story-viewer__panel {
  position: relative;
  background: var(--surface, #16181d);
  border: 1px solid var(--border, #2a2d35);
  border-radius: 8px;
  overflow: auto;
}
.story-viewer--modal .story-viewer__panel {
  max-width: 720px;
  width: 90%;
  max-height: 85vh;
}
.story-viewer__head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 0.5rem 0.75rem;
  border-bottom: 1px solid var(--border, #2a2d35);
  font-weight: 600;
}
.story-viewer__close {
  background: none;
  border: none;
  color: inherit;
  cursor: pointer;
  font-size: 1rem;
}
.story-viewer__body {
  padding: 0.75rem;
}
.story-viewer__empty {
  opacity: 0.6;
  font-style: italic;
}
.story-viewer__world {
  margin-top: 0.75rem;
  border-top: 1px dashed var(--border, #2a2d35);
  padding-top: 0.5rem;
}
.story-viewer__world-title {
  margin: 0 0 0.4rem;
  font-size: 0.8rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  opacity: 0.7;
}
.story-viewer__world-list {
  margin: 0;
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 0.2rem 0.75rem;
}
.story-viewer__world-list dt {
  font-weight: 600;
  font-family: monospace;
}
.story-viewer__world-list dd {
  margin: 0;
}
.story-viewer__world-list pre {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
