<template>
  <div v-if="text" class="collapsible-text">
    <span class="ct-label">{{ label }}</span>
    <div
      v-if="markdown"
      class="ct-pre ct-markdown"
      data-testid="collapsible-markdown"
      v-html="renderedMarkdown"
    ></div>
    <pre v-else class="ct-pre">{{ displayed }}</pre>
    <button v-if="isTruncated(text)" class="ct-toggle" @click="expanded = !expanded">
      {{ expanded ? 'Show less' : 'Show full' }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from "vue";
import { isTruncated, maybeShow } from "./lib.js";
import { renderMarkdownDocument } from "../../lib/markdown.js";

const props = withDefaults(
  defineProps<{ label: string; text: string; markdown?: boolean }>(),
  { markdown: false }
);

const expanded = ref(true);

const displayed = computed(() => maybeShow(props.text, expanded.value));
const markdown = computed(() => props.markdown);
const renderedMarkdown = computed(() => renderMarkdownDocument(displayed.value));
</script>

<style scoped>
.collapsible-text {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}

.ct-label {
  color: var(--k-fg-muted, #64748b);
  font-size: 0.75rem;
}

.ct-pre {
  background: var(--k-bg-inset, #080f1a);
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

.ct-markdown {
  font-family:
    -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial,
    sans-serif;
  font-size: 0.82rem;
  color: var(--k-fg, #e2e8f0);
  white-space: normal;
}

.ct-markdown :deep(.md-h1) { font-size: 1.1rem; font-weight: 700; margin: 0 0 0.5rem; color: var(--k-fg, #f8fafc); }
.ct-markdown :deep(.md-h2) { font-size: 1rem; font-weight: 700; margin: 0.75rem 0 0.4rem; color: var(--k-fg, #f8fafc); }
.ct-markdown :deep(.md-h3),
.ct-markdown :deep(.md-h4),
.ct-markdown :deep(.md-h5),
.ct-markdown :deep(.md-h6) { font-size: 0.9rem; font-weight: 650; margin: 0.65rem 0 0.35rem; color: var(--k-fg, #f8fafc); }
.ct-markdown :deep(.md-p) { margin: 0 0 0.55rem; line-height: 1.55; }
.ct-markdown :deep(.md-ul),
.ct-markdown :deep(.md-ol) { margin: 0 0 0.55rem; padding-left: 1.35rem; line-height: 1.55; }
.ct-markdown :deep(.md-ul li),
.ct-markdown :deep(.md-ol li) { margin: 0.18rem 0; }
.ct-markdown :deep(.md-blockquote) { margin: 0 0 0.55rem; padding: 0.35rem 0.7rem; border-left: 3px solid var(--k-border-subtle, #334155); background: rgba(148, 163, 184, 0.08); color: var(--k-fg-muted, #cbd5e1); }
.ct-markdown :deep(.md-hr) { margin: 0.75rem 0; border: none; border-top: 1px solid var(--k-border, #1e293b); }
.ct-markdown :deep(.md-pre) { margin: 0 0 0.55rem; padding: 0.6rem 0.75rem; background: #030712; border: 1px solid var(--k-border, #1e293b); border-radius: 5px; overflow-x: auto; white-space: pre; font-size: 0.75rem; line-height: 1.45; }
.ct-markdown :deep(.md-pre code) { background: none; padding: 0; color: inherit; font-size: inherit; }
.ct-markdown :deep(.md-table) { width: 100%; border-collapse: collapse; margin: 0 0 0.65rem; font-size: 0.78rem; }
.ct-markdown :deep(.md-table th),
.ct-markdown :deep(.md-table td) { border: 1px solid var(--k-border, #1e293b); padding: 0.25rem 0.4rem; vertical-align: top; }
.ct-markdown :deep(.md-table th) { background: rgba(148, 163, 184, 0.12); color: var(--k-fg, #f8fafc); font-weight: 650; }
.ct-markdown :deep(code) { font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace; background: var(--k-bg-input, #1e293b); border-radius: 4px; padding: 0.08em 0.32em; color: var(--k-fg-code, #7dd3fc); }
.ct-markdown :deep(strong) { font-weight: 700; color: var(--k-fg, #f8fafc); }
.ct-markdown :deep(em) { font-style: italic; }
.ct-markdown :deep(a) { color: var(--k-fg-accent, #60a5fa); text-decoration: underline; }

.ct-toggle {
  align-self: flex-start;
  background: none;
  border: 1px solid var(--k-border-subtle, #334155);
  color: var(--k-fg-accent, #60a5fa);
  cursor: pointer;
  font-size: 0.72rem;
  padding: 0.15rem 0.5rem;
  border-radius: 3px;
}

.ct-toggle:hover {
  background: var(--k-bg-hover, #1e293b);
}
</style>
