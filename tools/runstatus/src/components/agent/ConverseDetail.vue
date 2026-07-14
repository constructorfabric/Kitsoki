<template>
  <div class="converse-detail">
    <div v-if="messages.length > 0" class="converse-detail__chat">
      <span class="od-label">Messages</span>
      <div class="converse-detail__log">
        <div
          v-for="(msg, i) in messages"
          :key="i"
          class="converse-detail__bubble"
          :class="bubbleClass(msg.role)"
        >
          <span class="converse-detail__role">{{ msg.role }}</span>
          <pre class="converse-detail__text">{{ msgText(msg) }}</pre>
        </div>
      </div>
    </div>

    <!-- ── Spatial attachment: what the operator pointed at ──────────────────
         The recorded input.visual block (frame by handle, point, resolved
         element + bbox). The frame rides BY HANDLE — we render a downscaled
         thumbnail via DataSource.artifactUrl and an element chip; the full
         bundle below is the auditable record. Absent on a no-visual call
         (the compat case renders exactly as before). -->
    <div v-if="visual" class="converse-detail__visual" data-testid="visual-attachment">
      <span class="od-label">Operator pointed at</span>
      <div class="converse-detail__visual-row">
        <img
          v-if="frameThumbUrl"
          class="converse-detail__visual-thumb"
          data-testid="visual-thumb"
          :src="frameThumbUrl"
          :alt="`frame ${visual.frame_handle}`"
        />
        <div class="converse-detail__visual-meta">
          <span
            v-if="elementChip"
            class="converse-detail__visual-chip"
            data-testid="visual-element-chip"
          >{{ elementChip }}</span>
          <span class="converse-detail__visual-coords" data-testid="visual-point">
            ({{ visual.point?.x ?? 0 }}, {{ visual.point?.y ?? 0 }})
          </span>
          <span v-if="visual.route" class="converse-detail__visual-route">{{ visual.route }}</span>
        </div>
      </div>
      <pre class="converse-detail__visual-bundle" data-testid="visual-bundle">{{ prettyJson(visual) }}</pre>
    </div>

    <CollapsibleText label="System Prompt" :text="systemPrompt" />
    <CollapsibleText label="Prompt" :text="prompt" />
    <CollapsibleText label="Final Response" :text="responseText" markdown />
  </div>
</template>

<script setup lang="ts">
import { computed } from "vue";
import type { TraceEvent } from "../../types.js";
import CollapsibleText from "./CollapsibleText.vue";
import { usePromptLoader } from "./usePromptLoader.js";
import { prettyJson } from "./lib.js";
import { createDataSource } from "../../data/source.js";

const props = defineProps<{ event: TraceEvent }>();

interface ChatMessage {
  role: string;
  content?: unknown;
  [key: string]: unknown;
}

/** The recorded input.visual block (docs/tracing/trace-format.md). Every field
 *  is optional and the bbox is positional [x,y,w,h], matching host.VisualAmbient's
 *  recorded shape. */
interface VisualInput {
  schema_version?: number;
  frame_handle?: string;
  media_handle?: string;
  point?: { x: number; y: number };
  t_ms?: number;
  route?: string;
  element?: {
    selector?: string;
    role?: string;
    text?: string;
    bbox?: number[];
  };
}

const attrs = computed(() => props.event.attrs);

const visual = computed<VisualInput | null>(() => {
  const inp = attrs.value.input as Record<string, unknown> | undefined;
  const v = inp?.visual;
  return v && typeof v === "object" ? (v as VisualInput) : null;
});

// The frame rides by handle; the thumbnail requests a downscaled still (heavy
// full-res is reserved for click-to-zoom — proposal open question 3).
const frameThumbUrl = computed(() => {
  const h = visual.value?.frame_handle;
  return h ? createDataSource().artifactUrl(h, 320) : "";
});

// A compact element chip: "<selector> (button \"Run\")", omitting empty clauses.
const elementChip = computed(() => {
  const el = visual.value?.element;
  if (!el) return "";
  const sel = el.selector || "(unnamed)";
  const attrsList: string[] = [];
  if (el.role) attrsList.push(el.role);
  if (el.text) attrsList.push(`"${el.text}"`);
  return attrsList.length ? `${sel} (${attrsList.join(" ")})` : sel;
});

const messages = computed<ChatMessage[]>(() => {
  const inp = attrs.value.input as Record<string, unknown> | undefined;
  const msgs = inp?.messages;
  if (Array.isArray(msgs)) return msgs as ChatMessage[];
  return [];
});

const { prompt, systemPrompt } = usePromptLoader(attrs);

const response = computed(() => attrs.value.response as Record<string, unknown> | undefined);
const responseText = computed(() => {
  const r = response.value;
  return typeof r?.text === "string" ? r.text : "";
});

function bubbleClass(role: string): string {
  switch (role) {
    case "user": return "converse-detail__bubble--user";
    case "assistant": return "converse-detail__bubble--assistant";
    case "system": return "converse-detail__bubble--system";
    default: return "converse-detail__bubble--other";
  }
}

function msgText(msg: ChatMessage): string {
  if (typeof msg.content === "string") return msg.content;
  if (Array.isArray(msg.content)) {
    return msg.content
      .map((c) => (typeof c === "object" && c !== null && "text" in c ? String((c as Record<string, unknown>).text) : JSON.stringify(c)))
      .join("\n");
  }
  return JSON.stringify(msg, null, 2);
}
</script>

<style scoped>
.converse-detail {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.converse-detail__chat {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}

.converse-detail__log {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  max-height: 20rem;
  overflow-y: auto;
  padding: 0.3rem;
  background: var(--k-bg-deep, #080f1a);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 4px;
}

.converse-detail__bubble {
  padding: 0.3rem 0.5rem;
  border-radius: 4px;
  border-left: 3px solid transparent;
}

.converse-detail__bubble--user      { background: #0f1e38; border-left-color: #60a5fa; }
.converse-detail__bubble--assistant { background: #0a1a14; border-left-color: #34d399; }
.converse-detail__bubble--system    { background: #1a1020; border-left-color: #a78bfa; }
.converse-detail__bubble--other     { background: var(--k-bg-input, #1e293b); border-left-color: var(--k-fg-subtle, #475569); }

.converse-detail__role {
  font-size: 0.65rem;
  font-weight: 700;
  text-transform: uppercase;
  color: var(--k-fg-muted, #64748b);
  display: block;
  margin-bottom: 0.15rem;
}

.converse-detail__text {
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  color: var(--k-fg, #e2e8f0);
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
}

.od-label {
  color: var(--k-fg-muted, #64748b);
  font-size: 0.75rem;
}

.od-pre {
  background: var(--k-bg-deep, #080f1a);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 4px;
  padding: 0.4rem 0.6rem;
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  color: var(--k-fg-code, #7dd3fc);
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
  max-height: 14rem;
  overflow-y: auto;
}

/* ── Spatial attachment ──────────────────────────────────────────────────── */
.converse-detail__visual {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}

.converse-detail__visual-row {
  display: flex;
  gap: 0.5rem;
  align-items: flex-start;
}

.converse-detail__visual-thumb {
  max-width: 160px;
  max-height: 120px;
  border: 1px solid #1e293b;
  border-radius: 4px;
  background: #080f1a;
  object-fit: contain;
}

.converse-detail__visual-meta {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
}

.converse-detail__visual-chip {
  align-self: flex-start;
  background: #0a1728;
  border: 1px solid #334155;
  color: #93c5fd;
  border-radius: 4px;
  padding: 0.15rem 0.45rem;
  word-break: break-word;
}

.converse-detail__visual-coords {
  color: #64748b;
}

.converse-detail__visual-route {
  color: #475569;
  word-break: break-all;
}

.converse-detail__visual-bundle {
  background: #080f1a;
  border: 1px solid #1e293b;
  border-radius: 4px;
  padding: 0.4rem 0.6rem;
  font-family: ui-monospace, monospace;
  font-size: 0.68rem;
  color: #94a3b8;
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
  max-height: 12rem;
  overflow-y: auto;
}
</style>
