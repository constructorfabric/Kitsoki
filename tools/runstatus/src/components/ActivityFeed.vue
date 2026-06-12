<template>
  <!-- The agent's activity feed: 🧠 thoughts interleaved with tool calls, in
       arrival order. Rendering from ONE ordered list is what keeps a thought
       ABOVE the tools that follow it — two separate buckets pushed every
       thought to the bottom as tool calls accumulated. Shared by the main
       chat's live bubble + preserved activity AND the meta overlay, so the
       two surfaces present the agent's work identically. -->
  <template v-for="(item, i) in items" :key="i">
    <div v-if="item.kind === 'tool'" class="chat-activity__tool">
      <span class="chat-activity__tool-name">{{ item.tool }}</span>
      <span v-if="item.preview" class="chat-activity__tool-preview">{{ item.preview }}</span>
    </div>
    <div v-else class="chat-activity__thought" data-testid="activity-thought">
      <span class="chat-activity__brain">🧠</span>
      <div class="chat-activity__text" v-html="renderAgentMarkdown(item.text)"></div>
    </div>
  </template>
</template>

<script setup lang="ts">
import type { StreamItem } from "../lib/activity.js";
import { renderAgentMarkdown } from "../lib/markdown.js";

defineProps<{ items: StreamItem[] }>();
</script>

<style scoped>
/* Light-theme defaults match the main chat's "paper" agent bubble; a dark
   host (the meta overlay) re-tints via the --activity-* custom properties
   instead of forking the markup. */
.chat-activity__tool {
  display: flex;
  gap: 0.5em;
  font-size: 12px;
  font-family: ui-monospace, monospace;
  color: var(--activity-tool, #4b5563);
  margin: 2px 0;
}

.chat-activity__tool-name {
  flex: 0 0 auto;
  white-space: nowrap;
  font-weight: 600;
  color: var(--activity-tool-name, #2563eb);
}

.chat-activity__tool-preview {
  color: var(--activity-muted, #6b7280);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 60ch;
}

.chat-activity__thought {
  display: flex;
  align-items: flex-start;
  gap: 0.45em;
  margin: 4px 0;
}

.chat-activity__brain {
  flex: 0 0 auto;
  font-size: 13px;
  line-height: 1.55;
}

.chat-activity__text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas,
    "Liberation Mono", monospace;
  font-size: 12px;
  line-height: 1.55;
  color: var(--activity-text, #374151);
}

/* renderAgentMarkdown output (v-html) — style the formatted bits. A fenced
   block becomes a code box rather than leaking as literal ```json backticks. */
.chat-activity__text :deep(.cv-h) {
  font-weight: 700;
}
.chat-activity__text :deep(strong) {
  font-weight: 700;
}
.chat-activity__text :deep(code) {
  background: var(--activity-code-bg, #e6e9ef);
  border-radius: 4px;
  padding: 0.05em 0.3em;
}
.chat-activity__text :deep(.cv-pre) {
  background: #1e2430;
  color: #d6deeb;
  border: 1px solid var(--activity-border, #cfd4dd);
  border-radius: 6px;
  padding: 0.5rem 0.65rem;
  margin: 0.35rem 0;
  overflow-x: auto;
  white-space: pre;
  font-size: 12px;
  line-height: 1.45;
}
.chat-activity__text :deep(.cv-pre code) {
  background: none;
  padding: 0;
  color: inherit;
}
</style>
