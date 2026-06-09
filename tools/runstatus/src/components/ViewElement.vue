<script setup lang="ts">
import { computed } from "vue";
import type { ViewElement } from "../types.js";
import { createDataSource } from "../data/source.js";

// Resolve artifact URLs through the ambient DataSource (live or snapshot).
// createDataSource() is cheap: it reads window.__KITSOKI_SNAPSHOT__ once and
// returns the appropriate implementation. Called here at module initialisation
// so the result is shared across all ViewElement instances on the page.
const _ds = createDataSource();
function artifactUrl(handle: string): string {
  return _ds.artifactUrl(handle);
}

const props = defineProps<{ element: ViewElement }>();

const el = computed(() => props.element);

/**
 * Split a block of prose into paragraphs on blank lines. We deliberately do NOT
 * pull in a markdown library — the viewer renders trace content for inspection,
 * not rich documents. We support exactly two inline/block conventions: blank
 * lines separate paragraphs, and backtick-delimited spans become <code>.
 */
const paragraphs = computed<string[]>(() => {
  const src = el.value.Source ?? "";
  return src
    .split(/\n\s*\n/)
    .map((p) => p.trim())
    .filter((p) => p.length > 0);
});

/**
 * One paragraph split into alternating text / inline-code segments. Even
 * indices are plain text, odd indices are inline-code (the content between a
 * pair of backticks). An unmatched trailing backtick is treated as literal
 * text. Newlines inside a paragraph collapse to spaces.
 */
interface Seg {
  code: boolean;
  text: string;
}
function segments(para: string): Seg[] {
  const parts = para.split("`");
  const out: Seg[] = [];
  for (let i = 0; i < parts.length; i++) {
    const isCode = i % 2 === 1;
    // A trailing unmatched backtick leaves an empty final text segment; skip it.
    if (parts[i] === "" && isCode && i === parts.length - 1) continue;
    const text = isCode ? parts[i] : parts[i].replace(/\s*\n\s*/g, " ");
    out.push({ code: isCode, text });
  }
  return out;
}

const items = computed(() => el.value.Items ?? []);
const pairs = computed(() => el.value.Pairs ?? []);

/** Banner color → CSS modifier class. Falls back to a neutral box. */
const bannerClass = computed(() => {
  const c = (el.value.Color ?? "").toLowerCase();
  if (c === "error" || c === "danger" || c === "red") return "banner--error";
  if (c === "warn" || c === "warning" || c === "amber") return "banner--warn";
  if (c === "success" || c === "ok" || c === "green") return "banner--success";
  if (c === "info" || c === "blue") return "banner--info";
  return "banner--neutral";
});
</script>

<template>
  <!-- prose / template: paragraphs with minimal inline-code rendering. -->
  <template v-if="el.Kind === 'prose' || el.Kind === 'template'">
    <p v-for="(para, pi) in paragraphs" :key="pi" class="ve-prose">
      <template v-for="(seg, si) in segments(para)" :key="si">
        <code v-if="seg.code" class="ve-inline-code">{{ seg.text }}</code>
        <template v-else>{{ seg.text }}</template>
      </template>
    </p>
  </template>

  <h3 v-else-if="el.Kind === 'heading'" class="ve-heading">{{ el.Source }}</h3>

  <pre v-else-if="el.Kind === 'code'" class="ve-code"><code>{{ el.Source }}</code></pre>

  <ul v-else-if="el.Kind === 'list'" class="ve-list">
    <li v-for="(item, ii) in items" :key="ii">
      <span class="ve-list-label">{{ item.Label }}</span>
      <span v-if="item.Hint" class="ve-list-hint">{{ item.Hint }}</span>
    </li>
  </ul>

  <dl v-else-if="el.Kind === 'kv'" class="ve-kv">
    <template v-for="(pair, pi) in pairs" :key="pi">
      <dt class="ve-kv-key">{{ pair.Key }}</dt>
      <dd class="ve-kv-value">{{ pair.Value }}</dd>
    </template>
  </dl>

  <div
    v-else-if="el.Kind === 'banner'"
    class="ve-banner"
    :class="bannerClass"
    role="note"
  >
    <span v-if="el.Marker" class="ve-banner-marker">{{ el.Marker }}</span>
    <div class="ve-banner-body">
      <div class="ve-banner-text">{{ el.Source }}</div>
      <div v-if="el.Subtitle" class="ve-banner-subtitle">{{ el.Subtitle }}</div>
    </div>
  </div>

  <!-- choice elements are rendered as interactive buttons by InputBar; omit here to avoid duplication. -->

  <!-- media: dispatch on MIME family; fall back to a labeled download link. -->
  <div v-else-if="el.Kind === 'media'" class="ve-media">
    <template v-if="el.Handle">
      <!-- video/* → native player with Range-request support for seeking -->
      <video
        v-if="(el.Mime ?? '').startsWith('video/')"
        class="ve-media-video"
        controls
        preload="metadata"
        :src="artifactUrl(el.Handle)"
      >
        <span class="ve-media-fallback">
          Your browser does not support video playback.
          <a :href="artifactUrl(el.Handle)">Download</a>
        </span>
      </video>

      <!-- image/* → lazy-loaded image -->
      <img
        v-else-if="(el.Mime ?? '').startsWith('image/')"
        class="ve-media-image"
        loading="lazy"
        :src="artifactUrl(el.Handle)"
        :alt="el.Caption ?? el.Handle"
      />

      <!-- application/pdf → inline frame -->
      <iframe
        v-else-if="el.Mime === 'application/pdf'"
        class="ve-media-iframe"
        :src="artifactUrl(el.Handle)"
        :title="el.Caption ?? el.Handle"
      />

      <!-- text/html → sandboxed frame (no scripts, no same-origin access) -->
      <iframe
        v-else-if="el.Mime === 'text/html'"
        class="ve-media-iframe"
        sandbox
        :src="artifactUrl(el.Handle)"
        :title="el.Caption ?? el.Handle"
      />

      <!-- unknown MIME → labeled download link -->
      <a
        v-else
        class="ve-media-link"
        :href="artifactUrl(el.Handle)"
        :download="el.Handle"
      >{{ el.Caption ?? el.Handle }}</a>
    </template>

    <!-- caption / label rendered below any media element when present -->
    <p v-if="el.Caption" class="ve-media-caption">{{ el.Caption }}</p>
  </div>
</template>

<style scoped>
:host,
.ve-prose,
.ve-heading,
.ve-list,
.ve-kv,
.ve-banner {
  font-family:
    -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial,
    sans-serif;
  color: #1f2430;
  line-height: 1.55;
}

.ve-prose {
  margin: 0 0 0.85em;
  font-size: 15px;
}

.ve-prose:last-child {
  margin-bottom: 0;
}

.ve-inline-code,
.ve-code code {
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas,
    "Liberation Mono", monospace;
}

.ve-inline-code {
  background: #f0f1f4;
  border-radius: 4px;
  padding: 0.08em 0.35em;
  font-size: 0.9em;
  color: #b3306b;
}

.ve-heading {
  margin: 1.1em 0 0.5em;
  font-size: 18px;
  font-weight: 600;
  letter-spacing: -0.01em;
  color: #11151c;
}

.ve-code {
  margin: 0 0 0.85em;
  padding: 0.9em 1.1em;
  background: #1b1f27;
  color: #e6e9ef;
  border-radius: 8px;
  overflow-x: auto;
  font-size: 13.5px;
  line-height: 1.5;
}

.ve-code code {
  background: none;
  padding: 0;
  color: inherit;
}

.ve-list {
  margin: 0 0 0.85em;
  padding-left: 1.4em;
}

.ve-list li {
  margin: 0.25em 0;
  font-size: 15px;
}

.ve-list-hint {
  margin-left: 0.5em;
  color: #6b7280;
  font-size: 0.88em;
}

.ve-kv {
  display: grid;
  grid-template-columns: minmax(8rem, max-content) 1fr;
  gap: 0.3em 1.2em;
  margin: 0 0 0.85em;
  font-size: 15px;
}

.ve-kv-key {
  font-weight: 600;
  color: #4a5160;
}

.ve-kv-value {
  margin: 0;
  color: #1f2430;
}

.ve-banner {
  display: flex;
  gap: 0.75em;
  align-items: flex-start;
  margin: 0 0 0.85em;
  padding: 0.85em 1.1em;
  border-radius: 8px;
  border: 1px solid;
  font-size: 15px;
}

.ve-banner-marker {
  font-size: 1.1em;
  line-height: 1.4;
}

.ve-banner-text {
  font-weight: 500;
}

.ve-banner-subtitle {
  margin-top: 0.2em;
  font-size: 0.9em;
  opacity: 0.85;
}

.banner--neutral {
  background: #f6f7f9;
  border-color: #d8dbe2;
  color: #2b303b;
}

.banner--info {
  background: #eef4ff;
  border-color: #c0d4ff;
  color: #1d4ed8;
}

.banner--success {
  background: #ecfdf3;
  border-color: #b6ecc8;
  color: #1b7a3e;
}

.banner--warn {
  background: #fff8eb;
  border-color: #f5dca0;
  color: #92590a;
}

.banner--error {
  background: #fef2f2;
  border-color: #f5c2c2;
  color: #b42318;
}

/* ── Media element ─────────────────────────────────────────────────────── */

.ve-media {
  margin: 0 0 0.85em;
}

.ve-media-video {
  display: block;
  width: 100%;
  max-width: 100%;
  border-radius: 6px;
  background: #000;
}

.ve-media-image {
  display: block;
  max-width: 100%;
  border-radius: 6px;
}

.ve-media-iframe {
  display: block;
  width: 100%;
  height: 480px;
  border: 1px solid #d8dbe2;
  border-radius: 6px;
}

.ve-media-link {
  color: #1d4ed8;
  text-decoration: underline;
  font-size: 15px;
  word-break: break-all;
}

.ve-media-fallback {
  display: block;
  font-size: 14px;
  color: #6b7280;
  margin-top: 0.4em;
}

.ve-media-caption {
  margin: 0.4em 0 0;
  font-size: 13px;
  color: #6b7280;
  font-style: italic;
}
</style>
