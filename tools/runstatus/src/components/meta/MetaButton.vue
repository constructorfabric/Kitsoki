<template>
  <!-- Global meta-mode launcher: a fixed button + a dropdown of modes. Hidden
       in snapshot/artifact mode (no live engine to chat with). -->
  <div v-if="!isSnapshot" class="meta-launcher" data-testid="meta-launcher">
    <button
      class="meta-launcher__btn"
      data-testid="meta-button"
      :aria-expanded="dropdownOpen"
      title="Meta mode — edit or ask about this story / kitsoki"
      @click="toggleDropdown"
    >
      <span class="meta-launcher__spark">✦</span> Meta
      <span class="meta-launcher__caret">▾</span>
    </button>

    <div v-if="dropdownOpen" class="meta-launcher__menu" data-testid="meta-menu">
      <button
        v-for="m in uiModes"
        :key="m.key"
        class="meta-launcher__item"
        :class="{ 'meta-launcher__item--disabled': !isEnabled(m.key) }"
        :data-testid="`meta-mode-${testidFor(m.key)}`"
        :disabled="!isEnabled(m.key)"
        :title="isEnabled(m.key) ? m.hint : m.disabledHint"
        @click="choose(m.key)"
      >
        <span class="meta-launcher__item-label">{{ m.label }}</span>
        <span class="meta-launcher__item-hint">{{ m.hint }}</span>
      </button>

      <div class="meta-launcher__divider" role="separator"></div>

      <button
        class="meta-launcher__item"
        data-testid="meta-report-bug"
        title="File a bug — attaches a scrubbed network trace + session replay"
        :disabled="bugReport.status === 'capturing'"
        @click="reportBug"
      >
        <span class="meta-launcher__item-label">Report bug</span>
        <span class="meta-launcher__item-hint">
          Review a trace + session replay, then file an issue
        </span>
      </button>
    </div>

    <div
      v-if="showToast"
      class="meta-launcher__toast"
      data-testid="bug-report-toast"
    >
      <span v-if="bugReport.status === 'capturing'" data-testid="bug-toast-capturing">
        Capturing trace + session replay…
      </span>
      <template v-else-if="bugReport.status === 'filed' && bugReport.filed">
        <span data-testid="bug-toast-path">Filed: {{ bugReport.filed.path }}</span>
        <button
          class="meta-launcher__toast-link"
          data-testid="bug-toast-open"
          title="Copy the issue path"
          @click="openFiled"
        >
          [open]
        </button>
        <button
          class="meta-launcher__toast-link"
          data-testid="bug-toast-dismiss"
          @click="bugReport.reset()"
        >
          ✕
        </button>
      </template>
      <template v-else-if="bugReport.status === 'error'">
        <span data-testid="bug-toast-error">Bug report failed: {{ bugReport.error }}</span>
        <button
          class="meta-launcher__toast-link"
          data-testid="bug-toast-dismiss"
          @click="bugReport.reset()"
        >
          ✕
        </button>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { useRoute } from "vue-router";
import { LiveSource } from "../../data/live-source.js";
import { useMetaStore } from "../../stores/meta.js";
import { useBugReportStore } from "../../stores/bugReport.js";

// The three modes the web surface exposes. Availability is decided by the
// server's advertised mode set for the current scope (story.* need a running
// session; kitsoki.* are cross-app), so a mode is enabled iff the server lists
// it — we just give friendly labels here.
const uiModes = [
  {
    key: "story.edit",
    label: "Story edit",
    hint: "Edit this story's YAML — applies live",
    disabledHint: "Open a story first",
  },
  {
    key: "story.ask",
    label: "Story Q&A",
    hint: "Ask about the current story (read-only)",
    disabledHint: "Open a story first",
  },
  {
    key: "kitsoki.ask",
    label: "Kitsoki help",
    hint: "Ask about kitsoki itself (read-only)",
    disabledHint: "Unavailable",
  },
] as const;

const isSnapshot =
  (globalThis as typeof globalThis & { __KITSOKI_SNAPSHOT__?: unknown })
    .__KITSOKI_SNAPSHOT__ !== undefined;

const route = useRoute();
const meta = useMetaStore();
const bugReport = useBugReportStore();
const source = new LiveSource("/");

const dropdownOpen = ref(false);

const sessionId = computed(() => {
  const p = route.params.sessionId;
  return typeof p === "string" ? p : "";
});

function isEnabled(key: string): boolean {
  return meta.modes.some((m) => m.key === key);
}

function testidFor(key: string): string {
  return key.replace(/\./g, "-");
}

function toggleDropdown(): void {
  dropdownOpen.value = !dropdownOpen.value;
}

async function choose(key: string): Promise<void> {
  if (!isEnabled(key)) return;
  dropdownOpen.value = false;
  await meta.openMode(source, sessionId.value, key);
}

/**
 * File a bug. Snapshots the rrweb session replay + console/errors and previews
 * the scrubbed HAR, then opens the review modal; the server attaches the held
 * HAR on submit. trace_ref carries the current session id when present so the
 * issue links back to the run.
 */
async function reportBug(): Promise<void> {
  if (bugReport.status === "capturing") return;
  dropdownOpen.value = false;
  await bugReport.trigger({
    source,
    defaultTitle: "Bug report",
    severity: "med",
    traceRef: sessionId.value || undefined,
  });
}

// The toast is for capture-in-progress and the post-submit result. While the
// operator reviews (reviewing/submitting) the modal owns the surface.
const showToast = computed(
  () =>
    bugReport.status === "capturing" ||
    bugReport.status === "filed" ||
    bugReport.status === "error"
);

/** Best-effort "open": copy the filed issue path to the clipboard. */
async function openFiled(): Promise<void> {
  const path = bugReport.filed?.path;
  if (!path) return;
  try {
    await navigator.clipboard?.writeText(path);
  } catch {
    /* clipboard unavailable — non-fatal */
  }
}

async function refreshModes(): Promise<void> {
  meta.setSession(sessionId.value);
  if (!isSnapshot) await meta.loadModes(source, sessionId.value);
}

// Reload the available modes whenever the routed session changes (home ↔
// session views), so the dropdown's enabled set tracks the scope.
watch(sessionId, refreshModes, { immediate: true });

// Close the dropdown on an outside click.
function onDocClick(e: MouseEvent): void {
  const el = e.target as HTMLElement | null;
  if (el && el.closest(".meta-launcher")) return;
  dropdownOpen.value = false;
}
onMounted(() => document.addEventListener("click", onDocClick));
onUnmounted(() => document.removeEventListener("click", onDocClick));
</script>

<style scoped>
.meta-launcher {
  /* Bottom-right floating launcher. Deliberately NOT top-right: the session
     views put their Observe/Drive/Reload controls top-right, and a fixed
     element there would intercept clicks on them. */
  position: fixed;
  bottom: 1rem;
  right: 1rem;
  z-index: 900;
}

.meta-launcher__btn {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  background: #1d4ed8;
  color: #eef2ff;
  border: 1px solid #2563eb;
  border-radius: 999px;
  padding: 0.32rem 0.7rem;
  font-size: 0.78rem;
  font-weight: 600;
  cursor: pointer;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.35);
  transition: background 0.12s;
}
.meta-launcher__btn:hover {
  background: #2563eb;
}
.meta-launcher__spark {
  font-size: 0.7rem;
}
.meta-launcher__caret {
  font-size: 0.6rem;
  opacity: 0.8;
}

.meta-launcher__menu {
  position: absolute;
  right: 0;
  /* Opens upward — the launcher sits at the bottom of the viewport. */
  bottom: 100%;
  margin-bottom: 0.35rem;
  min-width: 16rem;
  background: #0d1b2a;
  border: 1px solid #1e293b;
  border-radius: 6px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
  overflow: hidden;
}

.meta-launcher__item {
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
  width: 100%;
  text-align: left;
  background: none;
  border: none;
  border-bottom: 1px solid #16202e;
  color: #e2e8f0;
  padding: 0.5rem 0.7rem;
  cursor: pointer;
}
.meta-launcher__item:last-child {
  border-bottom: none;
}
.meta-launcher__item:hover:not(.meta-launcher__item--disabled) {
  background: #15233a;
}
.meta-launcher__item--disabled {
  color: #475569;
  cursor: not-allowed;
}
.meta-launcher__item-label {
  font-size: 0.8rem;
  font-weight: 600;
}
.meta-launcher__item-hint {
  font-size: 0.68rem;
  color: #64748b;
}
.meta-launcher__item:disabled {
  opacity: 0.5;
  cursor: progress;
}

.meta-launcher__divider {
  height: 1px;
  background: #1e293b;
}

.meta-launcher__toast {
  position: absolute;
  right: 0;
  bottom: 100%;
  margin-bottom: 0.35rem;
  max-width: 22rem;
  display: flex;
  align-items: center;
  gap: 0.4rem;
  background: #0d1b2a;
  border: 1px solid #1e293b;
  border-radius: 6px;
  padding: 0.45rem 0.6rem;
  font-size: 0.72rem;
  color: #e2e8f0;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
}
.meta-launcher__toast-link {
  background: none;
  border: none;
  color: #60a5fa;
  cursor: pointer;
  font-size: 0.72rem;
  padding: 0;
}
.meta-launcher__toast-link:hover {
  text-decoration: underline;
}
</style>
