<template>
  <div ref="scrollEl" class="chat-transcript" data-testid="chat-transcript">
    <div
      v-for="(entry, i) in transcript"
      :key="i"
      class="chat-row"
      :class="`chat-row--${entry.role}`"
      :data-testid="`chat-row-${entry.role}`"
    >
      <div class="chat-avatar" :class="`chat-avatar--${entry.role}`">
        {{ entry.role === "user" ? "U" : "A" }}
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
          <div class="chat-role">{{ entry.role === "user" ? "You" : "Agent" }}</div>
          <div
            v-if="entry.role === 'agent' && entry.isOffRamp"
            class="chat-offramp-chip"
            data-testid="offramp-chip"
          >
            ↪ off path
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
        <div v-else class="chat-text">{{ entry.text }}</div>
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
          <span class="chat-routing__tier" :class="`chat-routing__tier--${entry.routing!.routedBy}`"
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
</template>

<script setup lang="ts">
import { ref, watch, nextTick, onMounted } from "vue";
import type { View } from "../types.js";
import type { StreamItem } from "../lib/activity.js";
import type { RoutingInfo } from "../stores/run.js";
import ActivityDisclosure from "./ActivityDisclosure.vue";
import ViewElement from "./ViewElement.vue";
import { renderAgentMarkdown } from "../lib/markdown.js";

export interface ChatEntry {
  role: "user" | "agent";
  text: string;
  typedView?: View;
  /** The turn's preserved thinking/tool feed (collapsed activity section). */
  stream?: StreamItem[];
  /** True when this agent bubble is an off-ramp ("offpath") converse answer. */
  isOffRamp?: boolean;
  /** Routing provenance for a free-text user turn (renders the routing chip). */
  routing?: RoutingInfo;
}

/** Tooltip: the full routing story in one line. */
function routingTitle(r: RoutingInfo): string {
  const bits = [`routed to "${r.intent ?? "?"}" via the ${r.routedBy} tier`];
  if (r.matchType) bits.push(`(${r.matchType})`);
  if (r.confidence) bits.push(`confidence ${r.confidence.toFixed(2)}`);
  if (r.routedBy !== "llm") bits.push("— deterministic, no LLM, $0");
  return bits.join(" ");
}

const props = defineProps<{ transcript: ChatEntry[] }>();

const scrollEl = ref<HTMLElement | null>(null);

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

async function scrollToBottom() {
  await nextTick();
  const el = scrollEl.value;
  if (el) el.scrollTop = el.scrollHeight;
}

onMounted(scrollToBottom);
watch(
  () => props.transcript.length,
  () => {
    void scrollToBottom();
  },
);
</script>

<style scoped>
.chat-transcript {
  display: flex;
  flex-direction: column;
  gap: 16px;
  overflow-y: auto;
  padding: 20px 24px;
  height: 100%;
  box-sizing: border-box;
  background: #0f1115;
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
  background: #2563eb;
}

.chat-avatar--agent {
  background: #475569;
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
  /* Let the agent card grow to hold the 80-col view. */
  width: 100%;
  box-sizing: border-box;
}

.chat-bubble--user {
  background: #2563eb;
  color: #fff;
  border-bottom-right-radius: 4px;
}

/* Agent bubble is a light "paper" card on the dark chat pane: ViewElement
   renders its typed room-view elements with a light-theme palette (dark text,
   light banners, a dark code block), so the bubble must be light or the
   prose-heavy room views would render dark-on-dark and vanish. The plain-text
   fallback inherits this dark text too, so both render paths stay legible. */
.chat-bubble--agent {
  background: #f7f8fa;
  color: #1f2430;
  border: 1px solid #d8dbe2;
  border-bottom-left-radius: 4px;
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
/* The deterministic tiers are free; tint them green. The LLM tier is the only
   paid surface; tint it amber so the cost story reads at a glance. */
.chat-routing__tier--semantic,
.chat-routing__tier--deterministic,
.chat-routing__tier--turncache {
  background: #bef264;
}
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
  color: #11151c;
}
.chat-view :deep(strong) {
  font-weight: 700;
  color: #11151c;
}
.chat-view :deep(code) {
  background: #eceef2;
  border-radius: 4px;
  padding: 0.05em 0.3em;
  color: #b3306b;
}
/* Fenced code block (```json …```): a real code box, not raw backticks. The
   <pre> owns its own whitespace (white-space: pre), so it is exempt from the
   surrounding pre-wrap room-view layout. */
.chat-view :deep(.cv-pre) {
  background: #1e2430;
  border: 1px solid #cfd4dd;
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
  color: #d6deeb;
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
</style>
