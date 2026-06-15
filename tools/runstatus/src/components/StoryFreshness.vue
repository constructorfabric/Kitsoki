<template>
  <!-- Staleness indicator + reload button for the toolbar -->
  <div class="story-freshness" data-testid="story-freshness">
    <!-- Up-to-date: green check -->
    <span
      v-if="!stale && !checking"
      class="story-freshness__ok"
      title="Story definition is up-to-date with the file on disk"
      data-testid="freshness-ok"
    >✓ up to date</span>

    <!-- Checking spinner -->
    <span
      v-else-if="checking && !stale"
      class="story-freshness__checking"
      title="Checking for story changes…"
    >…</span>

    <!-- Stale: show summary chip + reload button -->
    <template v-else-if="stale">
      <button
        class="story-freshness__stale-chip"
        :title="'Story YAML has changed on disk — click to preview diff'"
        data-testid="freshness-stale"
        @click="openDiff"
      >
        ↻ story changed
      </button>
      <button
        class="story-freshness__reload-btn"
        :disabled="reloading"
        data-testid="freshness-reload"
        :title="'Reload the story definition in place (mirrors the TUI /reload)'"
        @click="openDiff"
      >
        {{ reloading ? "Reloading…" : "Reload" }}
      </button>
    </template>

    <!-- Diff modal -->
    <Teleport to="body">
      <div
        v-if="diffOpen"
        class="story-freshness__backdrop"
        data-testid="diff-modal-backdrop"
        @click.self="diffOpen = false"
      >
        <div class="story-freshness__modal" role="dialog" aria-modal="true" aria-label="Story diff">
          <div class="story-freshness__modal-header">
            <span class="story-freshness__modal-title">Story YAML diff</span>
            <button class="story-freshness__modal-close" @click="diffOpen = false" aria-label="Close">✕</button>
          </div>
          <p class="story-freshness__modal-summary">
            The story file on disk has changed since this session was loaded.
            Review the diff below and click <strong>Reload</strong> to apply it.
          </p>
          <div class="story-freshness__modal-diff-wrap">
            <pre class="story-freshness__diff"><template v-if="diff"><span
              v-for="(line, i) in diffLines"
              :key="i"
              :class="lineClass(line)"
            >{{ line }}</span></template><span v-else class="story-freshness__diff-empty">No diff available.</span></pre>
          </div>
          <div v-if="reloadError" class="story-freshness__modal-error">{{ reloadError }}</div>
          <div class="story-freshness__modal-footer">
            <button class="story-freshness__modal-cancel" @click="diffOpen = false">Cancel</button>
            <button
              class="story-freshness__modal-apply"
              :disabled="reloading"
              data-testid="diff-apply-reload"
              @click="onApplyReload"
            >{{ reloading ? "Reloading…" : "↻ Reload" }}</button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref, computed } from "vue";
import { LiveSource } from "../data/live-source.js";

const props = defineProps<{
  sessionId: string;
  /** Called after a successful reload so the parent can rehydrate. */
  onReloaded: (prevStateExists: boolean) => void;
  /** Called when reload fails so the parent can show a warning. */
  onReloadError?: (msg: string) => void;
}>();

const source = new LiveSource("/");

const stale = ref(false);
const diff = ref("");
const checking = ref(false);
const diffOpen = ref(false);
const reloading = ref(false);
const reloadError = ref<string | null>(null);

const diffLines = computed(() => diff.value.split("\n"));

function lineClass(line: string): string {
  if (line.startsWith("+++") || line.startsWith("---")) return "story-freshness__diff-meta";
  if (line.startsWith("@@")) return "story-freshness__diff-hunk";
  if (line.startsWith("+")) return "story-freshness__diff-add";
  if (line.startsWith("-")) return "story-freshness__diff-del";
  return "story-freshness__diff-ctx";
}

async function poll() {
  if (!props.sessionId) return;
  checking.value = true;
  try {
    const res = await source.checkStaleness(props.sessionId);
    stale.value = res.stale;
    if (res.stale) diff.value = res.diff;
  } catch {
    // silently ignore poll errors — network blip or unsupported surface
  } finally {
    checking.value = false;
  }
}

function openDiff() {
  reloadError.value = null;
  diffOpen.value = true;
}

async function onApplyReload() {
  if (reloading.value) return;
  reloading.value = true;
  reloadError.value = null;
  try {
    const res = await source.reloadSession(props.sessionId);
    stale.value = false;
    diff.value = "";
    diffOpen.value = false;
    props.onReloaded(res.prev_state_exists);
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e);
    reloadError.value = msg;
    props.onReloadError?.(msg);
  } finally {
    reloading.value = false;
  }
}

// Poll every 10 s; also poll immediately on mount.
let timer: ReturnType<typeof setInterval> | null = null;
onMounted(() => {
  void poll();
  timer = setInterval(poll, 10_000);
});
onUnmounted(() => {
  if (timer !== null) clearInterval(timer);
});
</script>

<style scoped>
.story-freshness {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  font-size: 0.75rem;
}

.story-freshness__ok {
  color: var(--k-success, #4ade80);
  font-weight: 600;
  white-space: nowrap;
}

.story-freshness__checking {
  color: var(--k-fg-muted, #64748b);
}

.story-freshness__stale-chip {
  background: #431407;
  border: 1px solid #c2410c;
  color: var(--k-warning, #fb923c);
  font-size: 0.72rem;
  font-weight: 600;
  padding: 0.1rem 0.45rem;
  border-radius: 4px;
  cursor: pointer;
  white-space: nowrap;
  font-family: inherit;
}

.story-freshness__stale-chip:hover {
  background: #5a1a07;
}

.story-freshness__reload-btn {
  background: var(--k-button-bg, #1e3a5f);
  border: 1px solid var(--k-border-focus, #3b82f6);
  color: var(--k-button-fg, #93c5fd);
  font-size: 0.72rem;
  font-weight: 600;
  padding: 0.1rem 0.5rem;
  border-radius: 4px;
  cursor: pointer;
  font-family: inherit;
}

.story-freshness__reload-btn:hover:not(:disabled) {
  background: var(--k-button-hover-bg, #1e40af);
}

.story-freshness__reload-btn:disabled {
  opacity: 0.5;
  cursor: default;
}

/* ── Diff modal ─────────────────────────────────────────────────────── */

.story-freshness__backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.65);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 500;
}

.story-freshness__modal {
  background: var(--k-bg, #0f172a);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 8px;
  width: min(720px, 92vw);
  max-height: 80vh;
  display: flex;
  flex-direction: column;
  box-shadow: 0 16px 48px rgba(0, 0, 0, 0.6);
}

.story-freshness__modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.75rem 1rem 0.5rem;
  border-bottom: 1px solid var(--k-border, #1e293b);
  flex-shrink: 0;
}

.story-freshness__modal-title {
  font-size: 0.875rem;
  font-weight: 600;
  color: var(--k-fg, #e2e8f0);
}

.story-freshness__modal-close {
  background: none;
  border: none;
  color: var(--k-fg-muted, #64748b);
  font-size: 0.875rem;
  cursor: pointer;
  padding: 0.1rem 0.3rem;
  line-height: 1;
}

.story-freshness__modal-close:hover {
  color: var(--k-fg, #e2e8f0);
}

.story-freshness__modal-summary {
  padding: 0.5rem 1rem;
  font-size: 0.8125rem;
  color: var(--k-fg-muted, #94a3b8);
  margin: 0;
  flex-shrink: 0;
}

.story-freshness__modal-diff-wrap {
  flex: 1;
  overflow: auto;
  padding: 0 0.75rem 0.5rem;
  min-height: 0;
}

.story-freshness__diff {
  font-family: ui-monospace, "Cascadia Code", "Fira Code", monospace;
  font-size: 0.75rem;
  line-height: 1.55;
  display: block;
  background: var(--k-bg-deep, #060e1c);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 4px;
  padding: 0.5rem;
  overflow: auto;
  white-space: pre;
  margin: 0;
}

.story-freshness__diff-add { color: var(--k-success, #86efac); display: block; }
.story-freshness__diff-del { color: var(--k-error, #fca5a5); display: block; }
.story-freshness__diff-hunk { color: var(--k-fg-accent, #7dd3fc); display: block; }
.story-freshness__diff-meta { color: var(--k-fg-muted, #64748b); display: block; }
.story-freshness__diff-ctx { color: var(--k-fg-muted, #94a3b8); display: block; }
.story-freshness__diff-empty { color: var(--k-fg-subtle, #475569); display: block; }

.story-freshness__modal-error {
  padding: 0.4rem 1rem;
  font-size: 0.8rem;
  color: var(--k-error, #fca5a5);
  flex-shrink: 0;
}

.story-freshness__modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
  padding: 0.6rem 1rem;
  border-top: 1px solid var(--k-border, #1e293b);
  flex-shrink: 0;
}

.story-freshness__modal-cancel {
  background: var(--k-bg-input, #1e293b);
  border: 1px solid #334155;
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.8125rem;
  padding: 0.35rem 0.9rem;
  border-radius: 5px;
  cursor: pointer;
  font-family: inherit;
}

.story-freshness__modal-cancel:hover {
  background: #273549;
}

.story-freshness__modal-apply {
  background: var(--k-button-bg, #1e3a5f);
  border: 1px solid var(--k-border-focus, #3b82f6);
  color: var(--k-button-fg, #93c5fd);
  font-size: 0.8125rem;
  font-weight: 600;
  padding: 0.35rem 1rem;
  border-radius: 5px;
  cursor: pointer;
  font-family: inherit;
}

.story-freshness__modal-apply:hover:not(:disabled) {
  background: var(--k-button-hover-bg, #1e40af);
}

.story-freshness__modal-apply:disabled {
  opacity: 0.5;
  cursor: default;
}
</style>
