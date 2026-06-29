<template>
  <div
    class="aar"
    :class="rowClass"
    data-testid="agent-action-row"
    :data-kind="row.kind"
  >
    <!-- The guardrail / nudge / banner rows carry their own anchor testid on the
         header so the tour can target the decide arc's distinct host-injected
         steps (guardrail-row / nudge-row / banner-row). -->
    <div
      class="aar__header"
      :data-testid="kindTestid"
      :data-verdict="row.kind === 'guardrail' ? (row.isError ? 'rejected' : 'pass') : undefined"
      @click="expanded = !expanded"
    >
      <span class="aar__kind-chip" :class="chipClass"
        ><span v-if="kindIcon" class="aar__kind-icon">{{ kindIcon }}</span
        >{{ kindLabel }}</span
      >
      <span class="aar__title">{{ row.title }}</span>

      <span v-if="row.kind === 'guardrail'" class="aar__verdict" :class="verdictClass">
        {{ row.isError ? 'REJECTED' : 'PASS' }}
      </span>
      <span v-else-if="row.isError" class="aar__err-flag">ERR</span>

      <span class="aar__spacer" />
      <span v-if="tokenStr" class="aar__tokens">{{ tokenStr }}</span>
      <span v-if="costStr" class="aar__cost">{{ costStr }}</span>
      <span v-if="row.offsetMs > 0" class="aar__offset">+{{ fmtMs(row.offsetMs) }}</span>
      <span v-if="hasBody" class="aar__toggle">{{ expanded ? '−' : '+' }}</span>
    </div>

    <div v-if="expanded && hasBody" class="aar__body">
      <div v-if="row.input !== undefined" class="aar__section">
        <span class="aar__label">Input</span>
        <pre class="aar__pre">{{ inputStr }}</pre>
      </div>
      <div v-if="row.output" class="aar__section">
        <span class="aar__label" :class="{ 'aar__label--err': row.isError }">
          {{ outputLabel }}
        </span>
        <pre class="aar__pre" :class="{ 'aar__pre--err': row.isError }">{{ row.output }}</pre>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from "vue";
import { fmtMs, fmtTokens, fmtCost, prettyJson } from "./lib.js";
import { isClipBug } from "../../lib/clipBug.js";
import type { NormalizedEvent, NormalizedKind } from "../../data/transcript.js";

const props = defineProps<{ row: NormalizedEvent }>();

// Demo-only: ?clipBug=1 restores the since-fixed title-overflow regression so the
// bugfix-deck capture can record a before/after. Inert in every normal session.
const clipBug = isClipBug();

// Tool/mcp rows expand collapsed by default (the list stays scannable); the
// terminal result, reasoning, and host rows expand open so the operator sees the
// payload without a click. Guardrail rows open so the verdict is visible.
const expanded = ref(
  props.row.kind === "reasoning" ||
    props.row.kind === "result" ||
    props.row.kind === "guardrail" ||
    props.row.kind === "host-nudge" ||
    props.row.kind === "banner"
);

const hasBody = computed(
  () => props.row.input !== undefined || !!props.row.output
);

const inputStr = computed(() =>
  typeof props.row.input === "string"
    ? props.row.input
    : prettyJson(props.row.input)
);

const KIND_LABEL: Record<NormalizedKind, string> = {
  system: "SYS",
  reasoning: "THINK",
  tool: "TOOL",
  mcp: "MCP",
  guardrail: "GUARD",
  "host-nudge": "NUDGE",
  banner: "HOST",
  result: "RESULT",
};
// The "reasoning" kind covers two distinct things: genuine model THINKING
// (a `thinking` block / a buffered thinking-token run) and plain assistant
// narration TEXT. Only thinking earns the brain glyph + "THINK" chip, matching
// the TUI where 🧠 marks thinking and narration is plain text. We distinguish on
// the normalizer's titles ("Reasoning"/"Thinking" vs "Assistant").
const isThinking = computed(
  () =>
    props.row.kind === "reasoning" &&
    /^(Reasoning|Thinking)/.test(props.row.title)
);

const kindLabel = computed(() => {
  if (props.row.kind === "reasoning") return isThinking.value ? "THINK" : "TEXT";
  return KIND_LABEL[props.row.kind] ?? props.row.kind;
});

// Reasoning(thinking) rows carry a brain glyph, like the TUI's "🧠 …" lines.
const kindIcon = computed(() => (isThinking.value ? "🧠" : ""));

const outputLabel = computed(() => {
  switch (props.row.kind) {
    case "reasoning":
      return "Text";
    case "guardrail":
      return props.row.isError ? "Rejection reason" : "Verdict result";
    case "host-nudge":
      return "Nudge";
    case "result":
      return "Result";
    default:
      return "Output";
  }
});

const rowClass = computed(() => ({
  "aar--error": props.row.isError === true,
  "aar--clip-bug": clipBug,
  [`aar--${props.row.kind}`]: true,
}));

const chipClass = computed(() => `aar__kind-chip--${props.row.kind}`);
const verdictClass = computed(() => ({
  "aar__verdict--pass": props.row.isError !== true,
  "aar__verdict--fail": props.row.isError === true,
}));

// Distinct anchor testids for the decide-arc rows the tour spotlights.
const kindTestid = computed(() => {
  switch (props.row.kind) {
    case "guardrail":
      return "guardrail-row";
    case "host-nudge":
      return "nudge-row";
    case "banner":
      return "banner-row";
    default:
      return "agent-action-row-header";
  }
});

const tokenStr = computed(() => {
  const t = props.row.tokens;
  if (!t) return "";
  const parts: string[] = [];
  if (t.input !== undefined) parts.push(`in:${fmtTokens(t.input)}`);
  if (t.output !== undefined) parts.push(`out:${fmtTokens(t.output)}`);
  return parts.join(" ");
});
const costStr = computed(() => fmtCost(props.row.cost));
</script>

<style scoped>
.aar {
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 4px;
  overflow: hidden;
}

.aar--error {
  border-color: var(--k-error, #7f1d1d);
}

.aar--host-nudge {
  border-color: #831843;
  border-left: 3px solid #ec4899;
}

.aar--banner {
  border-color: #831843;
  border-style: dashed;
}

.aar__header {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.25rem 0.5rem;
  background: var(--k-bg-widget, #0a1728);
  cursor: pointer;
  font-size: 0.75rem;
}

.aar__header:hover {
  background: var(--k-bg-hover, #0f1e38);
}

.aar__kind-chip {
  padding: 0.05rem 0.35rem;
  border-radius: 3px;
  font-size: 0.62rem;
  font-weight: 700;
  font-family: ui-monospace, monospace;
  letter-spacing: 0.03em;
  white-space: nowrap;
}

.aar__kind-icon {
  margin-right: 0.2rem;
  font-size: 0.7rem;
  letter-spacing: 0;
}

.aar__kind-chip--system    { background: var(--k-bg-input, #1e293b); color: var(--k-fg-muted, #94a3b8); }
.aar__kind-chip--reasoning { background: #0c2a3e; color: var(--k-fg-accent, #7dd3fc); }
.aar__kind-chip--tool      { background: #3a2d08; color: #fde68a; }
.aar__kind-chip--mcp       { background: #2e1065; color: #d8b4fe; }
.aar__kind-chip--guardrail { background: #1e1b4b; color: #a5b4fc; }
.aar__kind-chip--host-nudge { background: #500724; color: #f9a8d4; }
.aar__kind-chip--banner    { background: #500724; color: #f9a8d4; }
.aar__kind-chip--result    { background: var(--k-success-bg, #042f1c); color: var(--k-success, #6ee7b7); }

.aar__title {
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  color: var(--k-fg, #e2e8f0);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  /* A flex item's implicit min-width is its content size, so without this a
     long title refuses to shrink and the ellipsis never engages — the title
     overflows the card and shoves the token/cost cluster off the row. */
  flex: 0 1 auto;
  min-width: 0;
}

/* Demo-only: ?clipBug=1 (see lib/clipBug.ts) restores the pre-fix state by
   reverting ONLY the fix — the title keeps its content-size minimum
   (min-width:auto), so as a flex child it can't shrink, the ellipsis never
   engages, and the over-wide title shoves the token/cost cluster past the card's
   right edge where .aar { overflow:hidden } clips it. This is exactly HEAD's
   pre-fix failure mode — no overflow:visible dramatization. The BEFORE half of
   the bugfix-deck before/after capture. */
.aar--clip-bug .aar__title {
  min-width: auto;
}

.aar__verdict {
  font-size: 0.62rem;
  font-weight: 700;
  font-family: ui-monospace, monospace;
  padding: 0.05rem 0.3rem;
  border-radius: 2px;
}

.aar__verdict--pass { background: var(--k-success-bg, #052e16); color: var(--k-success, #86efac); }
.aar__verdict--fail { background: #2d0707; color: var(--k-error, #fca5a5); }

.aar__err-flag {
  background: #7f1d1d;
  color: var(--k-error, #fca5a5);
  font-size: 0.6rem;
  padding: 0.05rem 0.25rem;
  border-radius: 2px;
}

.aar__spacer {
  flex: 1;
}

.aar__tokens {
  color: var(--k-fg-subtle, #475569);
  font-size: 0.65rem;
  font-family: ui-monospace, monospace;
  white-space: nowrap;
}

.aar__cost {
  color: #a3e635;
  font-size: 0.65rem;
  font-family: ui-monospace, monospace;
}

.aar__offset {
  color: var(--k-fg-subtle, #475569);
  font-size: 0.65rem;
  font-family: ui-monospace, monospace;
}

.aar__toggle {
  color: var(--k-fg-subtle, #475569);
  font-size: 0.75rem;
  min-width: 0.8rem;
  text-align: center;
}

.aar__body {
  padding: 0.35rem 0.5rem;
  background: var(--k-bg-inset, #080f1a);
  border-top: 1px solid var(--k-border, #1e293b);
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}

.aar__section {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}

.aar__label {
  color: var(--k-fg-muted, #64748b);
  font-size: 0.68rem;
}

.aar__label--err {
  color: var(--k-error, #f87171);
}

.aar__pre {
  background: var(--k-bg-inset, #080f1a);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 3px;
  padding: 0.3rem 0.5rem;
  font-family: ui-monospace, monospace;
  font-size: 0.7rem;
  color: var(--k-fg-code, #7dd3fc);
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
  max-height: 18rem;
  overflow-y: auto;
}

.aar__pre--err {
  color: var(--k-error, #fca5a5);
}
</style>
