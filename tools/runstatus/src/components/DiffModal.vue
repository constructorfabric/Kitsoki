<script setup lang="ts">
import { ref, onMounted, onUnmounted } from "vue";
import { JsonRpcClient } from "../transport/jsonrpc.js";
import UnifiedDiff from "./agent/UnifiedDiff.vue";

const props = defineProps<{ path: string }>();
const emit = defineEmits<{ close: [] }>();

const content = ref<string | null>(null);
const error = ref<string | null>(null);
const missing = ref(false);

const rpc = new JsonRpcClient("/");

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
  } catch (e) {
    const message = e instanceof Error ? e.message : String(e);
    if (isNotFound(message)) missing.value = true;
    else error.value = message;
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
      class="dm-backdrop"
      @click="onBackdropClick"
      role="dialog"
      aria-modal="true"
      data-testid="diff-artifact-modal"
    >
      <div class="dm-panel">
        <header class="dm-header">
          <span class="dm-path" :title="path" data-testid="diff-artifact-modal-path">{{ path }}</span>
          <button class="dm-close" @click="emit('close')" aria-label="Close" data-testid="diff-artifact-modal-close">x</button>
        </header>
        <div class="dm-body" data-testid="diff-artifact-modal-body">
          <div v-if="error" class="dm-error">Failed to load diff: {{ error }}</div>
          <div v-else-if="missing" class="dm-missing">Not written yet - this diff does not exist on disk.</div>
          <div v-else-if="content === null" class="dm-loading">Loading...</div>
          <UnifiedDiff v-else :diff="content" />
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.dm-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(2, 4, 8, 0.72);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1550;
}

.dm-panel {
  width: min(1080px, 94vw);
  height: min(760px, 90vh);
  background: var(--k-bg, #0f172a);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 8px;
  box-shadow: 0 18px 54px rgba(0, 0, 0, 0.56);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.dm-header {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.75rem 1rem;
  border-bottom: 1px solid var(--k-border, #1e293b);
  background: var(--k-bg-panel, #111827);
  flex-shrink: 0;
}

.dm-path {
  flex: 1;
  color: var(--k-fg-muted, #94a3b8);
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas,
    "Liberation Mono", monospace;
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dm-close {
  background: none;
  border: none;
  color: var(--k-fg-muted, #94a3b8);
  cursor: pointer;
  font-size: 16px;
  line-height: 1;
  padding: 0.2rem 0.4rem;
  border-radius: 4px;
}

.dm-close:hover {
  color: var(--k-fg, #e2e8f0);
  background: var(--k-bg-hover, #1e293b);
}

.dm-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 0.75rem;
}

.dm-loading,
.dm-missing,
.dm-error {
  color: var(--k-fg-muted, #94a3b8);
  font-size: 14px;
}

.dm-error {
  color: var(--k-error, #fca5a5);
}
</style>
