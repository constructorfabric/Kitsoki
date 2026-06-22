<script setup lang="ts">
import { computed, ref } from "vue";
import { useRoute } from "vue-router";
import type { ViewElement } from "../types.js";
import { createDataSource } from "../data/source.js";
import MarkdownModal from "./MarkdownModal.vue";

// Current route — used only to recover the active sessionId so a rendered video
// can link to its /review feedback surface (the room view renders inline in
// /s/:sessionId; the flag-a-moment UI lives at /review/:sessionId).
// useRoute() is undefined when the component is mounted without a router
// (e.g. unit tests); the review link is simply absent there.
const _route = useRoute();
const _sessionId = computed<string>(() => {
  const sid = _route?.params?.sessionId;
  return typeof sid === "string" ? sid : "";
});

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

// ── Media normalisation ──────────────────────────────────────────────────────
// The engine (internal/app.ViewElement) and any future producer may send EITHER
// the SPA-native {Handle, Mime, Caption} shape OR the engine-native
// {MediaHandle, MediaCaption, MediaKind} shape. Normalise to one accessor set so
// the template's MIME-family dispatch works for both. MIME is derived from
// MediaKind when no explicit Mime is present (the engine selects by kind, not a
// MIME string), with a video/* default for the common walkthrough case.
const KIND_MIME: Record<string, string> = {
  video: "video/mp4",
  image: "image/png",
  pdf: "application/pdf",
  html: "text/html",
  slideshow: "text/html",
};
const mediaHandle = computed<string>(() => el.value.Handle ?? el.value.MediaHandle ?? "");
const mediaCaption = computed<string>(() => el.value.Caption ?? el.value.MediaCaption ?? "");
// "Open in review" affordance: when a video renders inside a live session view,
// link to the /review feedback surface for that handle. Absent off-session
// (snapshot / artifact mode) where there is no sessionId to scrub against.
const reviewHref = computed<string | null>(() => {
  const sid = _sessionId.value;
  if (!sid || !mediaHandle.value) return null;
  return `#/review/${encodeURIComponent(sid)}?video=${encodeURIComponent(
    mediaHandle.value
  )}`;
});
const mediaMime = computed<string>(() => {
  if (el.value.Mime) return el.value.Mime;
  const kind = (el.value.MediaKind ?? "").toLowerCase();
  if (kind && KIND_MIME[kind]) return KIND_MIME[kind];
  // Fall back on the handle's stem hint (…#hash carries no extension), then the
  // common case: a rendered walkthrough is an mp4. A wrong guess only changes
  // which player branch renders; /artifact/{id} sets the real Content-Type.
  return "video/mp4";
});

/**
 * Split a block of prose into paragraphs on blank lines. We deliberately do NOT
 * pull in a markdown library — the viewer renders trace content for inspection,
 * not rich documents. We support exactly three inline/block conventions: blank
 * lines separate paragraphs, backtick-delimited spans become <code>, and
 * **double-asterisk** spans become <strong>. The TUI's Glamour pipeline and the
 * main-chat plain-text fallback (lib/markdown.ts) both render bold, so a typed
 * prose/template element must too — otherwise authored `**emphasis**` leaks to
 * the operator as literal asterisks.
 */
const paragraphs = computed<string[]>(() => {
  const src = el.value.Source ?? "";
  return src
    .split(/\n\s*\n/)
    .map((p) => p.trim())
    .filter((p) => p.length > 0);
});

/**
 * One paragraph split into typed inline segments. Backtick pairs delimit
 * inline-code (rendered verbatim — never re-scanned for bold, mirroring
 * markdown's "code spans are literal" rule); outside code, double-asterisk
 * pairs delimit bold. Newlines inside a paragraph collapse to spaces. An
 * unmatched trailing backtick / `**` leaves an empty final segment, which is
 * dropped.
 */
interface Seg {
  kind: "text" | "code" | "bold";
  text: string;
}
function segments(para: string): Seg[] {
  const out: Seg[] = [];
  const parts = para.split("`");
  for (let i = 0; i < parts.length; i++) {
    const isCode = i % 2 === 1;
    // A trailing unmatched backtick leaves an empty final segment; skip it.
    if (parts[i] === "" && isCode && i === parts.length - 1) continue;
    if (isCode) {
      out.push({ kind: "code", text: parts[i] });
      continue;
    }
    // Outside code: collapse intra-paragraph newlines, then split bold runs.
    // Odd indices are between a pair of `**`; empty runs (adjacent markers or
    // an unmatched trailing `**`) render nothing, so drop them.
    const boldParts = parts[i].replace(/\s*\n\s*/g, " ").split("**");
    for (let j = 0; j < boldParts.length; j++) {
      if (boldParts[j] === "") continue;
      out.push({ kind: j % 2 === 1 ? "bold" : "text", text: boldParts[j] });
    }
  }
  return out;
}

const items = computed(() => el.value.Items ?? []);
const pairs = computed(() => el.value.Pairs ?? []);

/** Path currently open in the markdown modal (null = closed). */
const openedPath = ref<string | null>(null);

function isMarkdownPath(value: string): boolean {
  return /\S+\.md$/.test(value.trim());
}

/** A literal hex accent (#rgb / #rrggbb / #rrggbbaa) authored on the banner. */
const HEX_RE = /^#(?:[0-9a-f]{3}|[0-9a-f]{6}|[0-9a-f]{8})$/i;
const bannerHex = computed<string>(() => {
  const c = (el.value.Color ?? "").trim();
  return HEX_RE.test(c) ? c : "";
});

/** Banner color → CSS modifier class. Named tokens map to a semantic box; a
 * literal hex accent is honoured inline (see bannerStyle) so the web conveys
 * the same per-phase colour the TUI's coloured rule does — the hex is authored
 * in the trace, so rendering it is faithful, not a UI override. */
const bannerClass = computed(() => {
  if (bannerHex.value) return "banner--accent";
  const c = (el.value.Color ?? "").toLowerCase();
  if (c === "error" || c === "danger" || c === "red") return "banner--error";
  if (c === "warn" || c === "warning" || c === "amber") return "banner--warn";
  if (c === "success" || c === "ok" || c === "green") return "banner--success";
  if (c === "info" || c === "blue") return "banner--info";
  return "banner--neutral";
});

/** Inline accent for a hex-coloured banner: the authored colour tints the
 * border + text and a faint wash of the background, matching the TUI's
 * per-phase coloured banner rule. Empty for named/absent colours. */
const bannerStyle = computed<Record<string, string>>((): Record<string, string> => {
  const c = bannerHex.value;
  if (!c) return {};
  return {
    borderColor: c,
    color: c,
    background: `color-mix(in srgb, ${c} 8%, var(--k-paper-bg, #f6f7f9))`,
  };
});
</script>

<template>
  <!-- prose / template: paragraphs with minimal inline-code rendering. -->
  <template v-if="el.Kind === 'prose' || el.Kind === 'template'">
    <p v-for="(para, pi) in paragraphs" :key="pi" class="ve-prose">
      <template v-for="(seg, si) in segments(para)" :key="si">
        <code v-if="seg.kind === 'code'" class="ve-inline-code">{{ seg.text }}</code>
        <strong v-else-if="seg.kind === 'bold'" class="ve-bold">{{ seg.text }}</strong>
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
      <dd class="ve-kv-value">
        <button
          v-if="isMarkdownPath(pair.Value)"
          class="ve-kv-file-link"
          @click="openedPath = pair.Value.trim()"
        >{{ pair.Value }}</button>
        <template v-else>{{ pair.Value }}</template>
      </dd>
    </template>
  </dl>

  <MarkdownModal
    v-if="openedPath !== null"
    :path="openedPath"
    @close="openedPath = null"
  />

  <div
    v-else-if="el.Kind === 'banner'"
    class="ve-banner"
    :class="bannerClass"
    :style="bannerStyle"
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
  <div v-else-if="el.Kind === 'media'" class="ve-media" data-testid="media-element">
    <template v-if="mediaHandle">
      <!-- video/* → native player with Range-request support for seeking -->
      <video
        v-if="mediaMime.startsWith('video/')"
        class="ve-media-video"
        data-testid="media-video"
        controls
        preload="metadata"
        :src="artifactUrl(mediaHandle)"
      >
        <span class="ve-media-fallback">
          Your browser does not support video playback.
          <a :href="artifactUrl(mediaHandle)">Download</a>
        </span>
      </video>

      <!-- video → "Open in review": the inline player is read-only; flagging a
           scene / time-range and dispatching feedback lives on the /review
           surface. Shown only inside a live session (reviewHref non-null). -->
      <a
        v-if="mediaMime.startsWith('video/') && reviewHref"
        class="ve-media-review-link"
        data-testid="media-review-link"
        :href="reviewHref"
      >Open in review — flag a scene or moment →</a>

      <!-- image/* → lazy-loaded image -->
      <img
        v-else-if="mediaMime.startsWith('image/')"
        class="ve-media-image"
        loading="lazy"
        :src="artifactUrl(mediaHandle)"
        :alt="mediaCaption || mediaHandle"
      />

      <!-- application/pdf → inline frame -->
      <iframe
        v-else-if="mediaMime === 'application/pdf'"
        class="ve-media-iframe"
        :src="artifactUrl(mediaHandle)"
        :title="mediaCaption || mediaHandle"
      />

      <!-- text/html → sandboxed frame (no scripts, no same-origin access) -->
      <iframe
        v-else-if="mediaMime === 'text/html'"
        class="ve-media-iframe"
        sandbox=""
        :src="artifactUrl(mediaHandle)"
        :title="mediaCaption || mediaHandle"
      />

      <!-- unknown MIME → labeled download link -->
      <a
        v-else
        class="ve-media-link"
        :href="artifactUrl(mediaHandle)"
        :download="mediaHandle"
      >{{ mediaCaption || mediaHandle }}</a>
    </template>

    <!-- caption / label rendered below any media element when present -->
    <p v-if="mediaCaption" class="ve-media-caption">{{ mediaCaption }}</p>
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
  color: var(--k-paper-fg, #1f2430);
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
  background: var(--k-bg-hover, #f0f1f4);
  border-radius: 4px;
  padding: 0.08em 0.35em;
  font-size: 0.9em;
  color: var(--k-fg-code, #b3306b);
}

.ve-bold {
  font-weight: 700;
  color: var(--k-paper-fg, #11151c);
}

.ve-heading {
  margin: 1.1em 0 0.5em;
  font-size: 18px;
  font-weight: 600;
  letter-spacing: -0.01em;
  color: var(--k-paper-fg, #11151c);
}

.ve-code {
  margin: 0 0 0.85em;
  padding: 0.9em 1.1em;
  background: var(--k-bg-deep, #1b1f27);
  color: var(--k-fg, #e6e9ef);
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
  color: var(--k-fg-muted, #6b7280);
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
  color: var(--k-fg-muted, #4a5160);
}

.ve-kv-value {
  margin: 0;
  color: var(--k-paper-fg, #1f2430);
}

.ve-kv-file-link {
  background: none;
  border: none;
  padding: 0;
  cursor: pointer;
  color: var(--k-button-bg, #1d4ed8);
  text-decoration: underline;
  font-size: inherit;
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas,
    "Liberation Mono", monospace;
  word-break: break-all;
  text-align: left;
}
.ve-kv-file-link:hover {
  color: var(--k-fg-accent, #1e40af);
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
  background: var(--k-paper-bg, #f6f7f9);
  border-color: var(--k-paper-border, #d8dbe2);
  color: var(--k-paper-fg, #2b303b);
}

/* A hex-accented banner: the authored colour rides on the inline :style
 * (bannerStyle). This class only supplies a neutral fallback for the rare
 * browser without color-mix support; the inline border-color/color win. */
.banner--accent {
  background: var(--k-paper-bg, #f6f7f9);
  border-color: var(--k-paper-border, #d8dbe2);
  color: var(--k-paper-fg, #2b303b);
}

.banner--info {
  background: var(--k-paper-bg, #eef4ff);
  border-color: var(--k-info, #c0d4ff);
  color: var(--k-info, #1d4ed8);
}

.banner--success {
  background: var(--k-paper-bg, #ecfdf3);
  border-color: var(--k-success, #b6ecc8);
  color: var(--k-success, #1b7a3e);
}

.banner--warn {
  background: var(--k-paper-bg, #fff8eb);
  border-color: var(--k-warning, #f5dca0);
  color: var(--k-warning, #92590a);
}

.banner--error {
  background: var(--k-paper-bg, #fef2f2);
  border-color: var(--k-error, #f5c2c2);
  color: var(--k-error, #b42318);
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
  border: 1px solid var(--k-paper-border, #d8dbe2);
  border-radius: 6px;
}

.ve-media-link {
  color: var(--k-button-bg, #1d4ed8);
  text-decoration: underline;
  font-size: 15px;
  word-break: break-all;
}

.ve-media-review-link {
  display: inline-block;
  margin-top: 0.5em;
  padding: 0.35em 0.75em;
  background: var(--k-button-bg, #1d4ed8);
  color: var(--k-button-fg, #fff);
  border-radius: 6px;
  font-size: 13px;
  font-weight: 500;
  text-decoration: none;
}
.ve-media-review-link:hover {
  background: var(--k-button-hover-bg, #1a43bd);
}

.ve-media-fallback {
  display: block;
  font-size: 14px;
  color: var(--k-fg-muted, #6b7280);
  margin-top: 0.4em;
}

.ve-media-caption {
  margin: 0.4em 0 0;
  font-size: 13px;
  color: var(--k-fg-muted, #6b7280);
  font-style: italic;
}
</style>
