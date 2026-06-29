<script setup lang="ts">
import { ref, onMounted, onUnmounted } from "vue";
import { renderMarkdownDocument } from "../lib/markdown.js";
import { JsonRpcClient } from "../transport/jsonrpc.js";

const props = defineProps<{ path: string; fullscreen?: boolean }>();
const emit = defineEmits<{ close: [] }>();

const content = ref<string | null>(null);
const error = ref<string | null>(null);
// `missing` is the graceful state for a path that is advertised in a view but
// has not been written yet (e.g. a planned output artifact). It is NOT an error
// — the file simply does not exist on disk yet — so we render a calm notice
// instead of a red "Failed to load file" banner.
const missing = ref(false);
const rendered = ref("");

const rpc = new JsonRpcClient("/");

/** True when the file-read failure is a plain "file does not exist" error. */
function isNotFound(message: string): boolean {
  return /no such file or directory|cannot find the file|does not exist/i.test(
    message,
  );
}

onMounted(async () => {
  try {
    const result = await rpc.post<{ content: string }>("runstatus.file.read", {
      path: props.path,
    });
    content.value = result.content;
    rendered.value = renderMarkdownDocument(result.content);
  } catch (e) {
    const message = e instanceof Error ? e.message : String(e);
    if (isNotFound(message)) {
      missing.value = true;
    } else {
      error.value = message;
    }
  }
});

function onKeydown(e: KeyboardEvent) {
  if (e.key === "Escape") emit("close");
}

onMounted(() => document.addEventListener("keydown", onKeydown));
onUnmounted(() => document.removeEventListener("keydown", onKeydown));

function onBackdropClick(e: MouseEvent) {
  if (e.target === e.currentTarget) emit("close");
}
</script>

<template>
  <Teleport to="body">
    <div
      class="mm-backdrop"
      :class="{ 'mm-fullscreen': fullscreen }"
      @click="onBackdropClick"
      role="dialog"
      aria-modal="true"
      data-testid="markdown-modal"
    >
      <div class="mm-panel">
        <header class="mm-header">
          <span class="mm-path" :title="path" data-testid="markdown-modal-path">{{ path }}</span>
          <button class="mm-close" @click="emit('close')" aria-label="Close" data-testid="markdown-modal-close">✕</button>
        </header>
        <div class="mm-body" data-testid="markdown-modal-body">
          <div v-if="error" class="mm-error">Failed to load file: {{ error }}</div>
          <div v-else-if="missing" class="mm-missing">
            Not written yet — this file does not exist on disk. It will appear
            here once the step that produces it has run.
          </div>
          <div v-else-if="content === null" class="mm-loading">Loading…</div>
          <div v-else class="mm-md" v-html="rendered" />
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.mm-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.45);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
}

.mm-panel {
  background: var(--k-paper-bg, #fff);
  border-radius: 10px;
  box-shadow: 0 8px 40px rgba(0, 0, 0, 0.22);
  display: flex;
  flex-direction: column;
  width: min(860px, 92vw);
  max-height: 85vh;
  overflow: hidden;
}

/* Fullscreen variant: the artifact fills the stage (demo "full-screened via the
   modal" — see ArtifactModal). Larger panel + darker backdrop for presence. */
.mm-backdrop.mm-fullscreen { background: rgba(2, 4, 8, 0.82); }
.mm-fullscreen .mm-panel {
  width: 94vw;
  height: 92vh;
  max-height: 92vh;
}
.mm-fullscreen .mm-body { padding: 2em 3em; }
.mm-fullscreen .mm-md { max-width: 980px; margin: 0 auto; }

.mm-header {
  display: flex;
  align-items: center;
  gap: 0.75em;
  padding: 0.75em 1.1em;
  border-bottom: 1px solid var(--k-paper-border, #e5e7eb);
  background: var(--k-bg-widget, #f6f7f9);
  border-radius: 10px 10px 0 0;
  flex-shrink: 0;
}

.mm-path {
  flex: 1;
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas,
    "Liberation Mono", monospace;
  font-size: 12px;
  color: var(--k-fg-muted, #4a5160);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mm-close {
  background: none;
  border: none;
  cursor: pointer;
  font-size: 16px;
  color: var(--k-fg-muted, #6b7280);
  padding: 0.2em 0.4em;
  border-radius: 4px;
  line-height: 1;
  flex-shrink: 0;
}
.mm-close:hover {
  background: var(--k-bg-hover, #e5e7eb);
  color: var(--k-paper-fg, #1f2430);
}

.mm-body {
  overflow-y: auto;
  padding: 1.5em 2em;
  flex: 1;
}

.mm-loading,
.mm-missing,
.mm-error {
  color: var(--k-fg-muted, #6b7280);
  font-size: 14px;
}

.mm-missing {
  font-style: italic;
}

.mm-error {
  color: var(--k-error, #b42318);
}

/* Rendered markdown styles */
.mm-md :deep(.md-h1) { font-size: 1.7em; font-weight: 700; margin: 0 0 0.6em; color: var(--k-paper-fg, #11151c); }
.mm-md :deep(.md-h2) { font-size: 1.35em; font-weight: 600; margin: 1.2em 0 0.5em; color: var(--k-paper-fg, #11151c); border-bottom: 1px solid var(--k-paper-border, #e5e7eb); padding-bottom: 0.25em; }
.mm-md :deep(.md-h3) { font-size: 1.1em; font-weight: 600; margin: 1em 0 0.4em; color: var(--k-paper-fg, #11151c); }
.mm-md :deep(.md-h4),
.mm-md :deep(.md-h5),
.mm-md :deep(.md-h6) { font-size: 1em; font-weight: 600; margin: 0.8em 0 0.35em; color: var(--k-paper-fg, #1f2430); }
.mm-md :deep(.md-p) { margin: 0 0 0.85em; font-size: 15px; line-height: 1.65; color: var(--k-paper-fg, #1f2430); }
.mm-md :deep(.md-ul),
.mm-md :deep(.md-ol) { margin: 0 0 0.85em; padding-left: 1.5em; font-size: 15px; line-height: 1.65; color: var(--k-paper-fg, #1f2430); }
.mm-md :deep(.md-ul li),
.mm-md :deep(.md-ol li) { margin: 0.3em 0; }
.mm-md :deep(.md-blockquote) { margin: 0 0 0.85em; padding: 0.5em 1em; border-left: 4px solid var(--k-paper-border, #d1d5db); background: var(--k-bg-widget, #f9fafb); color: var(--k-fg-muted, #4a5160); font-style: italic; }
.mm-md :deep(.md-hr) { margin: 1.2em 0; border: none; border-top: 1px solid var(--k-paper-border, #e5e7eb); }
.mm-md :deep(.md-pre) { margin: 0 0 0.85em; padding: 0.9em 1.1em; background: var(--k-bg-deep, #1b1f27); color: var(--k-fg, #e6e9ef); border-radius: 8px; overflow-x: auto; font-size: 13.5px; line-height: 1.5; }
.mm-md :deep(.md-pre code) { background: none; padding: 0; color: inherit; font-size: inherit; }
.mm-md :deep(code) { font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace; background: var(--k-bg-input, #f0f1f4); border-radius: 4px; padding: 0.08em 0.35em; font-size: 0.9em; color: #b3306b; }
.mm-md :deep(strong) { font-weight: 700; }
.mm-md :deep(em) { font-style: italic; }
.mm-md :deep(a) { color: var(--k-fg-accent, #1d4ed8); text-decoration: underline; }
.mm-md :deep(a:hover) { color: #1e40af; }
</style>
