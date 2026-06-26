<template>
  <div ref="scrollEl" class="chat-transcript" data-testid="chat-transcript">
   <div ref="contentEl" class="chat-transcript__inner">
    <div
      v-for="(entry, i) in transcript"
      :key="i"
      class="chat-row"
      :class="`chat-row--${entry.role}`"
      :data-testid="`chat-row-${entry.role}`"
    >
      <div class="chat-avatar" :class="`chat-avatar--${entry.role}`">
        {{ entry.role === "user" ? "U" : entry.role === "narration" ? "⚙" : "A" }}
      </div>
      <div
        class="chat-bubble"
        :class="[
          `chat-bubble--${entry.role}`,
          { 'chat-bubble--offramp': entry.role === 'agent' && entry.isOffRamp },
        ]"
        :data-testid="
          entry.role === 'agent' && entry.isOffRamp ? 'offramp-bubble' : undefined
        "
        :data-mode="entry.role === 'agent' && entry.isOffRamp ? 'offpath' : undefined"
      >
        <!-- An off-ramp answer is a free-form converse reply that did NOT
             advance state (TurnResult mode "offpath"). The chip sits beside the
             "Agent" role so a viewer (and vision-QA) can tell it apart from a
             normal room transition and from a rejection — at a glance, in the
             same header line. The menu still shows because state is unchanged. -->
        <div class="chat-role-row">
          <div class="chat-role">{{ entry.role === "user" ? "You" : entry.role === "narration" ? "Loop" : "Agent" }}</div>
          <div
            v-if="entry.role === 'agent' && entry.isOffRamp"
            class="chat-offramp-chip"
            data-testid="offramp-chip"
          >
            ↪ off path
          </div>
          <!-- Contextual-routing receipt: this agent turn was resolved by the
               CRR (contextual-routing) tier — the final routing tier that fires
               after deterministic + embedding miss. The chip names the lane /
               intent it landed in and marks the tier as "contextual" so an
               operator can tell a CRR decision apart from a normal transition.
               It also carries the stable decision_id (the rewind target) in its
               title, ahead of the rewind control's RPC being exposed. -->
          <div
            v-if="entry.role === 'agent' && entry.contextRoute"
            class="chat-route-receipt"
            data-testid="route-receipt"
            :title="routeReceiptTitle(entry.contextRoute!)"
          >
            <span class="chat-route-receipt__arrow">⤳</span>
            <span class="chat-route-receipt__target"
              >{{ routeTarget(entry.contextRoute!) }}</span
            >
            <span class="chat-route-receipt__tier">contextual</span>
            <span
              class="chat-route-receipt__conf"
              v-if="entry.contextRoute!.confidence"
              >{{ entry.contextRoute!.confidence.toFixed(2) }}</span
            >
            <!-- Rewind affordance: reverse this one CRR decision and re-dispatch
                 the original utterance. Disabled for an intent-class receipt —
                 the engine can't yet recover the original intent from the
                 journal, so we present a disabled control with an explanatory
                 tooltip rather than letting the operator trigger a server error. -->
            <button
              type="button"
              class="chat-route-receipt__rewind"
              data-testid="route-rewind-btn"
              :disabled="!canRewind(entry.contextRoute!)"
              :title="
                canRewind(entry.contextRoute!)
                  ? `rewind this route (decision ${entry.contextRoute!.decision_id})`
                  : 'rewind not available for this route yet'
              "
              @click="onRewind(entry.contextRoute!)"
            >
              ↺ rewind
            </button>
          </div>
        </div>
        <!-- The turn's preserved thinking/tool feed, collapsed by default so
             the final view leads but the activity that produced it stays one
             click away (matching the live bubble it replaces). -->
        <ActivityDisclosure v-if="entry.stream?.length" :items="entry.stream" />
        <div
          v-if="entry.role === 'agent' && hasElements(entry)"
          class="chat-elements"
        >
          <ViewElement
            v-for="(el, j) in entry.typedView!.Elements"
            :key="j"
            :element="el"
          />
        </div>
        <!-- Agent text is the engine's already-rendered room view: 80-col
             terminal layout (aligned key:value, numbered lists, indented
             sub-lines, hard wraps). The browser never evaluates pongo and must
             NOT re-flow that layout — doing so collapses lists into run-on
             prose. We preserve it verbatim (monospace + pre-wrap, faithful to
             the TUI) and only format inline bold/code + heading lines. -->
        <div
          v-else-if="entry.role === 'agent'"
          class="chat-view"
          v-html="renderView(entry.text)"
        ></div>
        <template v-else>
          <!-- Media annotation attached to this user turn: a marked-up frame of
               the deck the operator pointed at, rendered like an attached
               screenshot above their instruction. The poster <img> is hidden if
               the producer has no still (e.g. a live slidey deck), leaving the
               labeled "pointed at" chip. -->
          <div
            v-if="entry.role === 'user' && entry.annotation"
            class="chat-annotation"
            data-testid="chat-annotation"
          >
            <img
              v-if="!brokenPosters.has(entry.annotation.mediaHandle)"
              class="chat-annotation__img"
              :src="annotationPoster(entry.annotation.mediaHandle)"
              :alt="annotationLabel(entry.annotation)"
              @error="brokenPosters.add(entry.annotation!.mediaHandle)"
            />
            <span class="chat-annotation__chip"
              >📍 Pointed at: {{ annotationLabel(entry.annotation) }}</span
            >
          </div>
          <div class="chat-text">{{ entry.text }}</div>
        </template>
        <!-- Inline routing chip: how this free-text turn was resolved to an
             intent (tier + match reason + confidence). Surfaces the semantic-
             routing layer in the web chat the way the TUI does. -->
        <div
          v-if="entry.role === 'user' && entry.routing"
          class="chat-routing"
          data-testid="routing-chip"
          :title="routingTitle(entry.routing!)"
        >
          <span class="chat-routing__arrow">→</span>
          <span class="chat-routing__intent" v-if="entry.routing!.intent"
            >{{ entry.routing!.intent }}</span
          >
          <span
            class="chat-routing__tier"
            :class="[
              `chat-routing__tier--${entry.routing!.routedBy}`,
              entry.routing!.routedBy === 'llm'
                ? 'chat-routing__tier--paid'
                : 'chat-routing__tier--free',
            ]"
            >{{ entry.routing!.routedBy }}</span
          >
          <span class="chat-routing__reason" v-if="entry.routing!.matchType"
            >{{ entry.routing!.matchType }}</span
          >
          <span class="chat-routing__conf" v-if="entry.routing!.confidence"
            >{{ entry.routing!.confidence.toFixed(2) }}</span
          >
        </div>
      </div>
    </div>
   </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, watch, nextTick, onMounted, onBeforeUnmount } from "vue";
import type { View, ContextRouteInfo } from "../types.js";
import type { StreamItem } from "../lib/activity.js";
import type { RoutingInfo } from "../stores/run.js";
import ActivityDisclosure from "./ActivityDisclosure.vue";
import ViewElement from "./ViewElement.vue";
import { renderAgentMarkdown } from "../lib/markdown.js";
import { createDataSource } from "../data/source.js";

// Resolve the annotation poster URL through the ambient DataSource.
const _ds = createDataSource();
// Media handles whose poster still 404'd (e.g. a live slidey deck has none):
// once a poster <img> errors we stop trying to show it and keep just the chip.
const brokenPosters = reactive(new Set<string>());

/** A short label for the element/region a user annotation pointed at. */
function annotationLabel(a: ChatEntry["annotation"]): string {
  if (!a) return "annotation";
  const t = a.anchor.target;
  if (t?.kind === "semantic_element") return t.label || t.ref;
  return t?.kind ?? "annotation";
}

/** The poster-still URL for an annotation's media (empty when the source has no
 *  poster resolver — the <img> then errors out and only the chip shows). */
function annotationPoster(handle: string): string {
  return _ds.artifactPosterUrl?.(handle) ?? "";
}

export interface ChatEntry {
  role: "user" | "agent" | "narration";
  text: string;
  typedView?: View;
  /** The turn's preserved thinking/tool feed (collapsed activity section). */
  stream?: StreamItem[];
  /** True when this agent bubble is an off-ramp ("offpath") converse answer. */
  isOffRamp?: boolean;
  /** Routing provenance for a free-text user turn (renders the routing chip). */
  routing?: RoutingInfo;
  /** A media annotation the operator attached to this user turn (deck frame +
   *  picked anchor) — rendered as a marked-up thumbnail above the instruction. */
  annotation?: {
    mediaHandle: string;
    anchor: import("../lib/annotationAnchor.js").AnnotationAnchor;
  };
  /**
   * The contextual-routing receipt, set on an agent bubble when the CRR tier
   * resolved this turn (renders the "⤳ … · contextual" route receipt chip).
   */
  contextRoute?: ContextRouteInfo;
}

/** Tooltip: the full routing story in one line. */
function routingTitle(r: RoutingInfo): string {
  const bits = [`routed to "${r.intent ?? "?"}" via the ${r.routedBy} tier`];
  if (r.matchType) bits.push(`(${r.matchType})`);
  if (r.confidence) bits.push(`confidence ${r.confidence.toFixed(2)}`);
  if (r.routedBy !== "llm") bits.push("— deterministic, no LLM, $0");
  return bits.join(" ");
}

/**
 * routeTarget names what a CRR decision landed on: the matched intent for an
 * intent-class route, otherwise the lane (help / room_request / meta_edit) the
 * non-intent class resolved to. Falls back to the bare class.
 */
function routeTarget(r: ContextRouteInfo): string {
  if (r.class === "intent" && r.intent) return r.intent;
  return r.target_lane || r.class;
}

/** Tooltip: the full contextual-routing story + the decision id (rewind target). */
function routeReceiptTitle(r: ContextRouteInfo): string {
  const bits = [`contextually routed to "${routeTarget(r)}" (class: ${r.class})`];
  if (r.confidence) bits.push(`confidence ${r.confidence.toFixed(2)}`);
  if (r.reason) bits.push(`— ${r.reason}`);
  bits.push(`[decision ${r.decision_id}]`);
  return bits.join(" ");
}

const props = defineProps<{ transcript: ChatEntry[] }>();

// 'rewind' is emitted with the receipt's decision_id when the operator clicks
// the rewind affordance on a (rewindable) route receipt; the owning surface
// drives the run store's rewindRoute action with it.
const emit = defineEmits<{ rewind: [decisionId: string] }>();

/**
 * A CRR receipt is rewindable only for the lane classes the engine can reverse
 * today (help / room_request / meta_edit). An intent-class decision isn't yet
 * recoverable from the journal, so its rewind control is disabled with a tooltip.
 */
function canRewind(r: ContextRouteInfo): boolean {
  return !!r.decision_id && r.class !== "intent";
}

/** Emit the rewind request for a rewindable receipt (no-op when disabled). */
function onRewind(r: ContextRouteInfo): void {
  if (!canRewind(r)) return;
  emit("rewind", r.decision_id);
}

const scrollEl = ref<HTMLElement | null>(null);
const contentEl = ref<HTMLElement | null>(null);

function hasElements(entry: ChatEntry): boolean {
  const els = entry.typedView?.Elements;
  return Array.isArray(els) && els.length > 0;
}

// renderView prepares the engine's rendered room view for display. Verbatim
// line structure (newlines, indentation, column alignment) is preserved by the
// .chat-view CSS (white-space: pre-wrap, monospace) so the operator's TUI view
// is reproduced faithfully — we never join/re-flow lines, which would collapse
// the engine's lists/tables into run-on prose. On top, the shared renderer
// formats inline **bold** / `code`, ATX headings, and — crucially — fenced code
// blocks (```json …```), so a raw fenced reply renders as a code box instead of
// leaking to the operator as literal backticks.
const renderView = renderAgentMarkdown;

// Auto-follow, reliably. The original code scrolled to the bottom exactly once
// per appended message (a single nextTick, then `scrollTop = scrollHeight`).
// That is fine for a short sidebar bubble but breaks in the popped-out editor
// panel: an agent reply there is tall and lays out in stages (the v-html room
// view, the typed ViewElements, the activity feed, monospace metrics), so when
// the one-shot scroll fires the scrollHeight is still short and the camera lands
// part-way up — the reply opens below the fold and the view looks "stuck". The
// reliable fix is to (a) keep re-pinning to the bottom while content GROWS, via
// a ResizeObserver on the inner content, and (b) only do so while the reader is
// actually at the bottom, so scrolling up to re-read is never yanked back down.
let pinned = true;
// Treat "within this many px of the bottom" as pinned — covers sub-pixel
// rounding and the gap below the last bubble without feeling sticky.
const NEAR_BOTTOM_PX = 48;

function isAtBottom(el: HTMLElement): boolean {
  return el.scrollHeight - el.clientHeight - el.scrollTop <= NEAR_BOTTOM_PX;
}

// Assigning `scrollTop` (not calling scrollTo) is deliberate: the demo recorder
// owns the camera by overriding THIS element's scrollTop setter to a no-op
// (tools/vscode-kitsoki/tests/_helpers/conversation.ts). Keeping the property
// assignment means recordings stay un-yanked while real sessions still follow.
function scrollToBottom() {
  const el = scrollEl.value;
  if (el) el.scrollTop = el.scrollHeight;
}

function onScroll() {
  const el = scrollEl.value;
  if (el) pinned = isAtBottom(el);
}

let ro: ResizeObserver | null = null;

onMounted(() => {
  const el = scrollEl.value;
  if (el) el.addEventListener("scroll", onScroll, { passive: true });
  if (contentEl.value && typeof ResizeObserver !== "undefined") {
    ro = new ResizeObserver(() => {
      if (pinned) scrollToBottom();
    });
    ro.observe(contentEl.value);
  }
  void nextTick(scrollToBottom);
});

onBeforeUnmount(() => {
  scrollEl.value?.removeEventListener("scroll", onScroll);
  ro?.disconnect();
});

watch(
  () => props.transcript.length,
  async () => {
    // A turn the reader just sent always brings them to the bottom; an agent
    // reply only follows if they were already pinned there.
    const last = props.transcript[props.transcript.length - 1];
    if (last?.role === "user") pinned = true;
    await nextTick();
    if (pinned) scrollToBottom();
  },
);
</script>

<style scoped>
/* The outer element is the scroll VIEWPORT only — a definite height with
   overflow. Keeping the flex column on a separate inner element gives the
   ResizeObserver a node whose size tracks the content (the viewport's own box
   never changes as messages arrive), which is what makes the auto-follow fire. */
.chat-transcript {
  overflow-y: auto;
  height: 100%;
  box-sizing: border-box;
  background: var(--k-bg-inset, #0f1115);
}

.chat-transcript__inner {
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding: 20px 24px;
  /* Fill the viewport when the transcript is short so the background reads as
     one surface; grow past it (driving the scroll) once it overflows. */
  min-height: 100%;
  box-sizing: border-box;
}

.chat-row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  max-width: 78%;
}

.chat-row--user {
  align-self: flex-end;
  flex-direction: row-reverse;
}

/* Agent rows carry the engine's 80-col room view, so they need most of the
   chat column width to render without re-wrapping the terminal layout. */
.chat-row--agent {
  align-self: flex-start;
  max-width: 98%;
}

.chat-avatar {
  flex: 0 0 auto;
  width: 32px;
  height: 32px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 13px;
  font-weight: 600;
  color: #fff;
  user-select: none;
}

.chat-avatar--user {
  background: var(--k-button-hover-bg, #2563eb);
}

.chat-avatar--agent {
  background: var(--k-fg-subtle, #475569);
}

/* Narration ("Loop") avatar: a machine `say:` breadcrumb from a self-driving
   run. Tinted violet (the loop/automation accent) to set it apart from the
   operator (blue) and the agent (slate) at a glance. */
.chat-avatar--narration {
  background: #7c3aed;
  font-size: 15px;
}

/* Narration row: left-aligned like the agent, but a slim machine breadcrumb —
   not a full room-view card. */
.chat-row--narration {
  align-self: flex-start;
  max-width: 88%;
}

.chat-bubble {
  border-radius: 12px;
  padding: 10px 14px;
  font-size: 14px;
  line-height: 1.5;
  /* overflow-wrap (not word-break) so only over-long tokens break, never
     ordinary words mid-character. */
  overflow-wrap: anywhere;
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.25);
}

.chat-bubble--agent {
  /* Agent cards grow with their content; .chat-transcript owns vertical scrolling. */
  width: 100%;
  box-sizing: border-box;
}

.chat-bubble--user {
  background: var(--k-button-hover-bg, #2563eb);
  color: var(--k-button-fg, #fff);
  border-bottom-right-radius: 4px;
}

/* Agent bubble is a light "paper" card on the dark chat pane: ViewElement
   renders its typed room-view elements with a light-theme palette (dark text,
   light banners, a dark code block), so the bubble must be light or the
   prose-heavy room views would render dark-on-dark and vanish. The plain-text
   fallback inherits this dark text too, so both render paths stay legible. */
.chat-bubble--agent {
  background: var(--k-paper-bg, #f7f8fa);
  color: var(--k-paper-fg, #1f2430);
  border: 1px solid var(--k-paper-border, #d8dbe2);
  border-bottom-left-radius: 4px;
}

/* Narration ("Loop") bubble: a machine `say:` breadcrumb surfaced from the
   event log so a self-driving run still reads as a conversation. A compact,
   slightly translucent dark card with a violet left rule — distinct from the
   light agent room-view card and the blue operator bubble, so a viewer (and
   vision-QA) reads it as automated progress, not operator chat. */
.chat-bubble--narration {
  background: #171a2b;
  color: #e6e8f5;
  border: 1px solid #2c2f48;
  border-left: 3px solid #8b5cf6;
  border-bottom-left-radius: 4px;
  font-size: 13.5px;
  box-shadow: 0 1px 2px rgba(124, 92, 246, 0.18);
}
.chat-bubble--narration .chat-role {
  color: #c4b5fd;
  opacity: 0.95;
}

/* Off-ramp ("offpath") agent bubble: a free-form converse answer that did NOT
   advance state. Unlike a normal room-view bubble (which renders a menu
   transition) or a rejection, the whole bubble is tinted a soft violet and
   ringed with a distinct border so it reads as visually set-apart at a glance —
   without altering the answer text itself. The chip beside the role names it. */
.chat-bubble--offramp {
  background: #f6f3ff;
  border: 1px solid #c4b5fd;
  border-left: 4px solid #8b5cf6;
  box-shadow: 0 1px 2px rgba(139, 92, 246, 0.18);
}

/* Header line: the "Agent" role label and (when present) the off-path chip sit
   on one row so the off-path marker is read alongside the speaker, not stacked
   beneath it. */
.chat-role-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}

.chat-offramp-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 10px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: #ffffff;
  background: #7c3aed;
  border: 1px solid #6d28d9;
  border-radius: 999px;
  padding: 2px 9px;
  box-shadow: 0 1px 2px rgba(124, 58, 237, 0.3);
}

/* Contextual-routing receipt chip on the agent role row: a compact pill reading
   "⤳ target · contextual · conf". Sits beside the role label so a CRR decision
   is read alongside the speaker. Teal-tinted to set the contextual tier apart
   from the user-side routing chip and the violet off-path chip. */
.chat-route-receipt {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 10.5px;
  line-height: 1.2;
  color: #115e59;
  background: #d1faf3;
  border: 1px solid #5eead4;
  border-radius: 999px;
  padding: 2px 9px;
}
.chat-route-receipt__arrow {
  opacity: 0.7;
}
/* Rewind affordance: a compact text button that sits inside the receipt pill.
   Disabled (intent-class receipts the engine can't yet reverse) it dims and
   shows a not-allowed cursor, with the "not available yet" tooltip. */
.chat-route-receipt__rewind {
  font-family: inherit;
  font-size: 10px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: #115e59;
  background: transparent;
  border: 1px solid #5eead4;
  border-radius: 999px;
  padding: 0 6px;
  margin-left: 2px;
  cursor: pointer;
}
.chat-route-receipt__rewind:hover:not(:disabled) {
  background: #99f6e4;
}
.chat-route-receipt__rewind:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.chat-route-receipt__target {
  font-weight: 700;
}
.chat-route-receipt__tier {
  text-transform: uppercase;
  letter-spacing: 0.03em;
  font-weight: 600;
  font-size: 9.5px;
  background: #0d9488;
  color: #fff;
  border-radius: 4px;
  padding: 1px 5px;
}
.chat-route-receipt__conf {
  opacity: 0.75;
}

.chat-role {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  opacity: 0.6;
}

.chat-text {
  white-space: pre-wrap;
}

/* Inline routing chip under a user bubble: a compact pill row reading
   "→ intent · tier · reason · conf". Sits below the user text, right-aligned
   with the user column, in a muted monospace so it reads as provenance, not
   chat. */
.chat-routing {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 5px;
  margin-top: 7px;
  padding-top: 6px;
  border-top: 1px solid rgba(255, 255, 255, 0.18);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 10.5px;
  line-height: 1.2;
}
.chat-routing__arrow {
  color: rgba(255, 255, 255, 0.65);
}
.chat-routing__intent {
  font-weight: 700;
  color: #fff;
  background: rgba(255, 255, 255, 0.18);
  border-radius: 4px;
  padding: 1px 5px;
}
.chat-routing__tier {
  text-transform: uppercase;
  letter-spacing: 0.03em;
  font-weight: 600;
  border-radius: 4px;
  padding: 1px 5px;
  color: #0f1115;
  background: #cbd5e1;
}
/* Cost story, driven off the one fact that matters: did this turn spend?
   Every deterministic tier (semantic / deterministic / turncache / default /
   fallback / slot-fill) is free → tint green. The LLM tier is the only paid
   surface → amber. Keying the colour on the free/paid modifier (not an
   enumerated tier list) means a NEW deterministic tier — like the workbench
   free-form fallback — joins the green group automatically instead of
   silently rendering neutral. */
.chat-routing__tier--free {
  background: #bef264;
}
.chat-routing__tier--paid,
.chat-routing__tier--llm {
  background: #fcd34d;
}
.chat-routing__reason {
  color: rgba(255, 255, 255, 0.82);
}
.chat-routing__conf {
  color: rgba(255, 255, 255, 0.6);
  margin-left: auto;
}

/* The agent room view: preserve the engine's layout verbatim. Monospace +
   pre-wrap keeps aligned key:value columns, numbered lists and indentation
   intact; long lines soft-wrap at the bubble edge rather than re-flowing. */
.chat-view {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas,
    "Liberation Mono", monospace;
  font-size: 12.5px;
  line-height: 1.55;
  tab-size: 2;
}
/* renderAgentMarkdown output is injected via v-html, so its nodes carry no
   scoped-style attribute — target them with :deep so the rules apply. */
.chat-view :deep(.cv-h) {
  font-weight: 700;
  color: var(--k-paper-fg, #11151c);
}
.chat-view :deep(strong) {
  font-weight: 700;
  color: var(--k-paper-fg, #11151c);
}
.chat-view :deep(code) {
  background: var(--k-bg-hover, #eceef2);
  border-radius: 4px;
  padding: 0.05em 0.3em;
  color: var(--k-fg-code, #b3306b);
}
/* Fenced code block (```json …```): a real code box, not raw backticks. The
   <pre> owns its own whitespace (white-space: pre), so it is exempt from the
   surrounding pre-wrap room-view layout. */
.chat-view :deep(.cv-pre) {
  background: var(--k-bg-deep, #1e2430);
  border: 1px solid var(--k-paper-border, #cfd4dd);
  border-radius: 6px;
  padding: 0.55rem 0.7rem;
  margin: 0.4rem 0;
  overflow-x: auto;
  white-space: pre;
  font-size: 12px;
  line-height: 1.45;
}
.chat-view :deep(.cv-pre code) {
  background: none;
  padding: 0;
  color: var(--k-fg, #d6deeb);
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
}

.chat-elements {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

/* The collapsed activity feed (the turn's preserved thinking/tool stream)
   lives in ActivityDisclosure.vue / ActivityFeed.vue — shared with the meta
   overlay so both chats present the agent's work identically. */

/* Media annotation attached to a user turn — a marked-up frame thumbnail + the
   "pointed at" chip, rendered above the typed instruction like an attachment. */
.chat-annotation {
  display: flex;
  flex-direction: column;
  gap: 0.3em;
  margin-bottom: 0.4em;
}
.chat-annotation__img {
  max-width: 240px;
  max-height: 150px;
  border-radius: 6px;
  border: 1px solid var(--k-paper-border, #d8dbe2);
  object-fit: cover;
}
.chat-annotation__chip {
  align-self: flex-start;
  font-size: 12px;
  padding: 0.15em 0.5em;
  border-radius: 999px;
  background: var(--k-paper-bg, rgba(255, 255, 255, 0.12));
  border: 1px solid var(--k-paper-border, rgba(255, 255, 255, 0.2));
}
</style>
