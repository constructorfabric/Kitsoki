<script setup lang="ts">
import { computed, ref, onMounted, onBeforeUnmount, nextTick } from "vue";
import { useRoute } from "vue-router";
import { installEmbedViewListener } from "../lib/embedView.js";
import type { ViewElement } from "../types.js";
import { createDataSource } from "../data/source.js";
import type { AnnotationAnchor, MediaKind } from "../lib/annotationAnchor.js";
import MarkdownModal from "./MarkdownModal.vue";
import ArtifactAnnotator from "./ArtifactAnnotator.vue";
import { useRunStore } from "../stores/run.js";

// The live-run store owns the conversation transcript + turn streaming. Routing
// an annotation dispatch through it (rather than calling the data source
// directly) makes the annotation appear as a normal user message in the main
// chat and streams the agent's edit + re-render back as the reply.
const _run = useRunStore();

// Track which place the embedded artifact (e.g. the live deck) reports it is
// showing, via the generic `embed:view` postMessage protocol. The latest scope
// rides a refine as `current_scene` so the edit targets the slide the operator
// is actually looking at. Producer-neutral — kitsoki never interprets the scope.
let _teardownEmbedView: (() => void) | null = null;
function onKeydown(ev: KeyboardEvent): void {
  // Esc dismisses the composer (parking the draft), like a click outside it.
  if (ev.key === "Escape" && pendingAnchor.value) {
    ev.stopPropagation();
    dismissComposer();
  }
}
onMounted(() => {
  _teardownEmbedView = installEmbedViewListener((view) => _run.setEmbedView(view));
  window.addEventListener("keydown", onKeydown);
  void maybeAutoOpenAnnotate();
});
onBeforeUnmount(() => {
  _teardownEmbedView?.();
  _teardownEmbedView = null;
  window.removeEventListener("keydown", onKeydown);
});

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

function withQuery(url: string, params: Record<string, string>): string {
  const entries = Object.entries(params).filter(([, v]) => v !== "");
  if (entries.length === 0) return url;
  const sep = url.includes("?") ? "&" : "?";
  return `${url}${sep}${entries
    .map(([k, v]) => `${encodeURIComponent(k)}=${encodeURIComponent(v)}`)
    .join("&")}`;
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

// A `slideshow` media kind is a multi-scene deck (e.g. a slidey deck rendered to
// a self-contained HTML file). Inline display embeds the static HTML deck; when
// annotation is opened and a semantic sidecar exists, the annotator switches to
// the slidey poster+overlay substrate.
const isSlideshow = computed<boolean>(
  () => (el.value.MediaKind ?? "").toLowerCase() === "slideshow"
);
const slideshowUrl = computed<string>(() =>
  withQuery(artifactUrl(mediaHandle.value), {
    scene: _run.embedScope,
    step: _run.embedStep,
  })
);

// ── Annotate affordance (unified ArtifactAnnotator) ──────────────────────────
// A live media element (image / video / html / slideshow — never a pdf) can be
// annotated: clicking "Annotate" reveals the ArtifactAnnotator inline, which
// renders the right substrate for the media kind and emits one AnnotationAnchor.
// The anchor is dispatched as an anchored off-path note (ds.offpath) — the same
// path ReviewPage uses — so it actually leaves the client. Off-session
// (snapshot / artifact mode) there is no sessionId to anchor against, so the
// affordance is hidden.

/** The engine's MediaKind ("video"|"image"|"html"|"slideshow"|"pdf") maps onto
 *  the annotator's MediaKind union. pdf is intentionally absent — a pdf is not
 *  annotatable, so `mediaAnnotatable` gates it out before this is consulted. A
 *  sidecar-bearing artifact is promoted to "slidey" at annotate-open time (see
 *  openAnnotate) so the deck gets the SemanticOverlay. */
const ENGINE_MEDIA_KIND: Record<string, MediaKind> = {
  video: "mp4",
  image: "png",
  html: "html",
  slideshow: "html",
};

/** The annotator MediaKind from the MIME family, when the engine kind is absent
 *  or unmapped. Mirrors the template's MIME dispatch. */
function kindFromMime(mime: string): MediaKind {
  if (mime.startsWith("video/")) return "mp4";
  if (mime.startsWith("image/")) return "png";
  if (mime === "text/html") return "html";
  return "mp4";
}

/** Whether this media element offers the Annotate affordance: a resolvable
 *  handle, a live session to anchor against, and an annotatable kind (anything
 *  but a pdf — there is no annotation substrate for a pdf). */
const mediaAnnotatable = computed<boolean>(() => {
  if (!mediaHandle.value || !_sessionId.value) return false;
  const engineKind = (el.value.MediaKind ?? "").toLowerCase();
  if (engineKind === "pdf" || mediaMime.value === "application/pdf") return false;
  return true;
});

const annotateOpen = ref(false);
/** The MediaKind the annotator renders with once opened (slidey-promoted when a
 *  semantic sidecar exists for this handle). Null until openAnnotate resolves. */
const annotateKind = ref<MediaKind | null>(null);
const annotateBusy = ref(false);
const annotateSent = ref<string | null>(null);
const annotateError = ref<string | null>(null);
/** The anchor the operator just picked, held until they compose an instruction
 *  and Send. Null when nothing is staged (the picker is live). */
const pendingAnchor = ref<AnnotationAnchor | null>(null);
/** The composed instruction for the staged anchor. */
const instruction = ref("");

/** The intent annotations on this media dispatch (e.g. a deck's `refine`), and
 *  the slot the instruction rides. Empty intent ⇒ legacy off-path note. */
const annotateIntent = computed<string>(() => el.value.AnnotateIntent ?? "");
const annotateFeedbackSlot = computed<string>(
  () => el.value.AnnotateFeedbackSlot || "feedback"
);

/** A short human label for the staged anchor. */
function anchorLabel(anchor: AnnotationAnchor): string {
  return anchor.target?.kind === "semantic_element"
    ? anchor.target.label || anchor.target.ref
    : (anchor.target?.kind ?? "annotation");
}

/** A stable key for an anchor, used to remember a half-typed instruction per
 *  spot: dismissing the composer (click-outside / Esc) parks the draft here, and
 *  re-picking the SAME element restores it. */
function anchorKey(anchor: AnnotationAnchor): string {
  return anchor.target?.kind === "semantic_element"
    ? `el:${anchor.target.ref}`
    : JSON.stringify(anchor.target ?? {});
}
/** Per-spot parked instruction drafts (keyed by anchorKey). */
const drafts = ref<Record<string, string>>({});

/** Open the annotator. Probe the semantic sidecar ONCE: a non-null map means the
 *  media (even an mp4 deck) carries producer-declared elements, so render with
 *  the slidey path (poster backdrop + SemanticOverlay) regardless of the base
 *  artifact's MIME. Otherwise use the engine-kind / MIME-mapped kind. */
async function openAnnotate(): Promise<void> {
  annotateError.value = null;
  annotateSent.value = null;
  const engineKind = (el.value.MediaKind ?? "").toLowerCase();
  let kind: MediaKind =
    ENGINE_MEDIA_KIND[engineKind] ?? kindFromMime(mediaMime.value);
  // A slideshow is a multi-scene deck that speaks the embed protocol on its own
  // live surface — always annotate it via the `slidey` (live-embed) substrate so
  // the operator points at real elements on the slide (no static poster/sidecar
  // needed). Other kinds may still promote to slidey when a sidecar exists.
  if (engineKind === "slideshow") {
    kind = "slidey";
  } else {
    try {
      if (_ds.semanticMap) {
        const env = await _ds.semanticMap(_sessionId.value, mediaHandle.value);
        if (env && env.elements.length > 0) kind = "slidey";
      }
    } catch {
      /* no sidecar / probe failed — keep the MIME-mapped kind */
    }
  }
  annotateKind.value = kind;
  annotateOpen.value = true;
}

function shouldAutoOpenAnnotate(): boolean {
  if (typeof window === "undefined") return false;
  const raw = new URLSearchParams(window.location.hash.split("?")[1] ?? "").get("visual_annotate");
  if (!raw) return false;
  return raw === "1" || raw === "true" || raw === mediaHandle.value;
}

async function maybeAutoOpenAnnotate(): Promise<void> {
  await nextTick();
  if (!mediaAnnotatable.value || annotateOpen.value || !shouldAutoOpenAnnotate()) return;
  await openAnnotate();
}

function closeAnnotate(): void {
  annotateOpen.value = false;
  annotateKind.value = null;
  annotateError.value = null;
  pendingAnchor.value = null;
  instruction.value = "";
  drafts.value = {};
}

/** The annotator emitted an anchor — STAGE it (don't dispatch yet) so the
 *  operator can compose an instruction describing what they want changed before
 *  sending. Picking again before sending replaces the staged anchor. */
function onAnchor(anchor: AnnotationAnchor): void {
  if (annotateBusy.value) return;
  pendingAnchor.value = anchor;
  // Restore any instruction parked for THIS spot (re-picking the same element
  // brings back what you'd typed before dismissing); a fresh spot starts blank.
  instruction.value = drafts.value[anchorKey(anchor)] ?? "";
  annotateError.value = null;
  annotateSent.value = null;
}

/** Dismiss the composer WITHOUT discarding work: park the half-typed instruction
 *  under this spot's key so re-picking it restores the text, then unstage. Fired
 *  by a click outside the composer or Esc — the "close the input box" affordance.
 *  No-op when nothing is staged. */
function dismissComposer(): void {
  const anchor = pendingAnchor.value;
  if (!anchor || annotateBusy.value) return;
  const text = instruction.value.trim();
  if (text) drafts.value = { ...drafts.value, [anchorKey(anchor)]: instruction.value };
  else {
    const next = { ...drafts.value };
    delete next[anchorKey(anchor)];
    drafts.value = next;
  }
  pendingAnchor.value = null;
}

/** Discard the staged anchor AND its draft, returning to a clean pick. */
function clearPending(): void {
  const anchor = pendingAnchor.value;
  if (anchor) {
    const next = { ...drafts.value };
    delete next[anchorKey(anchor)];
    drafts.value = next;
  }
  pendingAnchor.value = null;
  instruction.value = "";
}

/** Send the staged anchor + composed instruction. When the media element
 *  declares an AnnotateIntent (e.g. a deck's `refine`), dispatch that intent
 *  with the instruction in its feedback slot and the anchor riding the turn, so
 *  the agent edits the pointed-at element. Otherwise fall back to a generic
 *  anchored off-path note. */
async function sendAnnotation(): Promise<void> {
  const anchor = pendingAnchor.value;
  if (!anchor || annotateBusy.value) return;
  const text = instruction.value.trim();
  // An intent dispatch needs an instruction (it's the feedback that drives the
  // edit); a bare off-path note can stand on the anchor alone.
  if (annotateIntent.value && !text) {
    annotateError.value = "Describe what you want changed, then Send.";
    return;
  }
  annotateBusy.value = true;
  annotateError.value = null;
  annotateSent.value = null;
  try {
    const label = anchorLabel(anchor);
    if (annotateIntent.value) {
      // Route through the run store (not _ds directly) so the dispatch shows up
      // in the MAIN chat as a normal user message — carrying the annotation
      // (deck frame + anchor) so it reads like an attached marked-up screenshot
      // — and the agent's edit + re-render stream back as the reply.
      await _run.submitIntent(
        _ds,
        _sessionId.value,
        annotateIntent.value,
        {
          [annotateFeedbackSlot.value]: text,
          // The slide the operator is looking at (from the deck's embed:view
          // reports), so the refine targets THAT slide — not a guessed default.
          ...(_run.embedScope ? { current_scene: _run.embedScope } : {}),
        },
        // No displayLabel: the user bubble derives its text from the feedback
        // slot. Passing the instruction as BOTH label and slot rendered it
        // twice ("<text>: <text>").
        undefined,
        {
          anchor,
          annotation: { mediaHandle: mediaHandle.value, anchor },
        }
      );
    } else {
      await _ds.offpath(
        _sessionId.value,
        text ? `${text} (re: ${label} on ${mediaHandle.value})` : `Annotated ${label} on ${mediaHandle.value}.`,
        undefined,
        anchor
      );
    }
    annotateSent.value = label;
    clearPending();
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e);
    // The session chat is single-writer: while a refine/turn is still running
    // its agent holds the chat lock, so a concurrent annotation dispatch comes
    // back as "chat busy: held by pid …". Surface that as an actionable hint
    // rather than the raw lock diagnostic.
    annotateError.value = /chat busy/i.test(msg)
      ? "The deck is still being refined — wait for the current edit to finish, then send your annotation again."
      : msg;
  } finally {
    annotateBusy.value = false;
  }
}

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
    <!-- While annotating, the ArtifactAnnotator below renders the annotatable
         substrate for this same handle; suppress the standalone player/iframe so
         the deck is embedded ONCE (not stacked as a second instance). -->
    <template v-if="mediaHandle && !annotateOpen">
      <!-- video/* → native player with Range-request support for seeking -->
      <video
        v-if="mediaMime.startsWith('video/')"
        :key="mediaHandle"
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
        :key="mediaHandle"
        class="ve-media-image"
        loading="lazy"
        :src="artifactUrl(mediaHandle)"
        :alt="mediaCaption || mediaHandle"
      />

      <!-- application/pdf → inline frame -->
      <iframe
        v-else-if="mediaMime === 'application/pdf'"
        :key="mediaHandle"
        class="ve-media-iframe"
        :src="artifactUrl(mediaHandle)"
        :title="mediaCaption || mediaHandle"
      />

      <!-- slideshow → embedded self-contained HTML deck + a direct-open link.
           The iframe stays sandboxed to an opaque origin but allows scripts so
           the static Slidey bundle can boot. MUST precede the text/html branch
           because a slideshow's MIME is text/html. -->
      <template v-else-if="isSlideshow">
        <!-- :key on the content-addressed handle forces Vue to REPLACE the
             iframe DOM node when a refine re-renders the deck (the handle's
             hash changes). Without it Vue patches src on the SAME element and
             the browser keeps showing the stale cached render — the user-visible
             "the deck never updates after I edit it" bug. -->
        <iframe
          :key="mediaHandle"
          class="ve-media-iframe"
          data-testid="media-slideshow-frame"
          sandbox="allow-scripts"
          :src="slideshowUrl"
          :title="mediaCaption || mediaHandle"
        />
        <a
          class="ve-media-review-link"
          data-testid="media-slideshow-open"
          :href="artifactUrl(mediaHandle)"
          target="_blank"
          rel="noopener"
        >Open the interactive deck →</a>
      </template>

      <!-- text/html → sandboxed frame with scripts, no same-origin access -->
      <iframe
        v-else-if="mediaMime === 'text/html'"
        :key="mediaHandle"
        class="ve-media-iframe"
        sandbox="allow-scripts"
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

    <!-- Annotate affordance: reveal the unified ArtifactAnnotator inline. The
         media kind is probed at open (a sidecar-bearing mp4 deck → slidey path
         with the SemanticOverlay over a poster); the emitted anchor dispatches
         as an anchored off-path note. Hidden off-session / for pdfs. -->
    <div v-if="mediaAnnotatable" class="ve-media-annotate">
      <button
        v-if="!annotateOpen"
        type="button"
        class="ve-media-annotate-trigger"
        data-testid="media-annotate"
        @click="openAnnotate"
      >Annotate</button>

      <div v-else class="ve-media-annotate-panel" data-testid="media-annotate-panel">
        <div class="ve-media-annotate-head">
          <span class="ve-media-annotate-title">Annotate · {{ annotateKind }}</span>
          <button
            type="button"
            class="ve-media-annotate-close"
            data-testid="media-annotate-close"
            @click="closeAnnotate"
          >Close</button>
        </div>
        <ArtifactAnnotator
          v-if="annotateKind"
          :ds="_ds"
          :session-id="_sessionId"
          :media-handle="mediaHandle"
          :media-kind="annotateKind"
          :poster-handle="mediaHandle"
          :live-embed="isSlideshow"
          :embed-scope="_run.embedScope"
          :embed-step="_run.embedStep"
          @anchor="onAnchor"
        />

        <!-- Click-outside backdrop: while the composer is open, a click anywhere
             off it dismisses the input box (parking any typed draft for re-pick),
             so the operator isn't forced to hunt for a button. -->
        <div
          v-if="pendingAnchor"
          class="ve-media-annotate-backdrop"
          data-testid="media-annotate-backdrop"
          @click="dismissComposer"
        ></div>

        <!-- Composer: once an anchor is picked, the operator describes what they
             want changed THERE, then Sends. This is what turns a pick into a
             real edit (the AnnotateIntent runs with this instruction). -->
        <div
          v-if="pendingAnchor"
          class="ve-media-annotate-composer"
          data-testid="media-annotate-composer"
        >
          <div class="ve-media-annotate-pointed-row">
            <span class="ve-media-annotate-pointed">
              Pointed at: <strong>{{ anchorLabel(pendingAnchor) }}</strong>
            </span>
            <button
              type="button"
              class="ve-media-annotate-dismiss"
              data-testid="media-annotate-dismiss"
              title="Dismiss (keeps your text for this spot) — Esc"
              aria-label="Dismiss"
              @click="dismissComposer"
            >×</button>
          </div>
          <textarea
            v-model="instruction"
            class="ve-media-annotate-input"
            data-testid="media-annotate-input"
            rows="2"
            :placeholder="annotateIntent
              ? 'Describe the change you want here…'
              : 'Add a note about this spot (optional)…'"
            :disabled="annotateBusy"
            @keydown.enter.exact.prevent="sendAnnotation"
          />
          <div class="ve-media-annotate-actions">
            <button
              type="button"
              class="ve-media-annotate-send"
              data-testid="media-annotate-send"
              :disabled="annotateBusy || (annotateIntent !== '' && instruction.trim() === '')"
              @click="sendAnnotation"
            >{{ annotateIntent ? "Send & refine" : "Send" }}</button>
            <button
              type="button"
              class="ve-media-annotate-repick"
              data-testid="media-annotate-repick"
              :disabled="annotateBusy"
              @click="clearPending"
            >Clear &amp; pick another</button>
          </div>
        </div>

        <p v-if="annotateBusy" class="ve-media-annotate-status">Sending annotation…</p>
        <p v-else-if="annotateSent" class="ve-media-annotate-status ve-media-annotate-ok">
          Annotation sent: {{ annotateSent }}
        </p>
        <p v-if="annotateError" class="ve-media-annotate-status ve-media-annotate-err">
          {{ annotateError }}
        </p>
      </div>
    </div>

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

.ve-media-annotate {
  margin-top: 0.5em;
}

.ve-media-annotate-trigger {
  background: var(--k-button-bg, #1d4ed8);
  color: var(--k-button-fg, #fff);
  border: none;
  border-radius: 6px;
  padding: 0.35em 0.85em;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
}
.ve-media-annotate-trigger:hover {
  background: var(--k-button-hover-bg, #1a43bd);
}

.ve-media-annotate-panel {
  position: relative;
  margin-top: 0.5em;
  padding: 0.6em;
  border: 1px solid var(--k-paper-border, #d8dbe2);
  border-radius: 8px;
  background: var(--k-paper-bg, #f6f7f9);
}

/* The composer FLOATS over the deck near where the operator pointed, instead of
   being stacked below the annotator. Anchored to the lower-centre of the stage
   as a compact card so it reads as "attached to" the annotation. */
.ve-media-annotate-composer {
  position: absolute;
  left: 50%;
  bottom: 3.25em;
  transform: translateX(-50%);
  z-index: 5;
  width: min(92%, 440px);
  display: flex;
  flex-direction: column;
  gap: 0.45em;
  padding: 0.6em 0.7em;
  border: 1px solid var(--k-paper-border, #cfd3db);
  border-radius: 10px;
  background: var(--k-paper-bg, #ffffff);
  box-shadow: 0 8px 28px rgba(15, 20, 30, 0.28);
}

/* Transparent click-catcher covering the whole panel while the composer is open;
   a click anywhere off the composer (which sits above it at a higher z-index)
   dismisses the input box. */
.ve-media-annotate-backdrop {
  position: absolute;
  inset: 0;
  z-index: 4;
  background: transparent;
  cursor: default;
}

.ve-media-annotate-pointed-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5em;
}
.ve-media-annotate-pointed {
  font-size: 12px;
  color: var(--k-fg-muted, #4a5160);
}
.ve-media-annotate-dismiss {
  flex: none;
  background: transparent;
  border: none;
  color: var(--k-fg-muted, #6b7280);
  font-size: 18px;
  line-height: 1;
  padding: 0 0.15em;
  cursor: pointer;
}
.ve-media-annotate-dismiss:hover {
  color: var(--k-fg, #1f2430);
}

.ve-media-annotate-input {
  width: 100%;
  box-sizing: border-box;
  resize: vertical;
  font: inherit;
  font-size: 13px;
  padding: 0.45em 0.55em;
  border: 1px solid var(--k-paper-border, #cfd3db);
  border-radius: 7px;
}

.ve-media-annotate-actions {
  display: flex;
  gap: 0.5em;
  justify-content: flex-end;
}

.ve-media-annotate-send {
  background: var(--k-accent, #2f6df0);
  color: #fff;
  border: none;
  border-radius: 6px;
  padding: 0.35em 0.85em;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
}

.ve-media-annotate-send:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.ve-media-annotate-repick {
  background: transparent;
  border: 1px solid var(--k-paper-border, #cfd3db);
  color: var(--k-fg-muted, #6b7280);
  border-radius: 6px;
  padding: 0.35em 0.7em;
  font-size: 12px;
  cursor: pointer;
}

.ve-media-annotate-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 0.5em;
}

.ve-media-annotate-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--k-fg-muted, #4a5160);
}

.ve-media-annotate-close {
  background: transparent;
  border: 1px solid var(--k-paper-border, #d8dbe2);
  color: var(--k-fg-muted, #6b7280);
  border-radius: 5px;
  padding: 0.2em 0.6em;
  font-size: 12px;
  cursor: pointer;
}

.ve-media-annotate-status {
  margin: 0.5em 0 0;
  font-size: 13px;
  color: var(--k-fg-muted, #6b7280);
}
.ve-media-annotate-ok {
  color: var(--k-success, #1b7a3e);
}
.ve-media-annotate-err {
  color: var(--k-error, #b42318);
}

.ve-media-caption {
  margin: 0.4em 0 0;
  font-size: 13px;
  color: var(--k-fg-muted, #6b7280);
  font-style: italic;
}
</style>
