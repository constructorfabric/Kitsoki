<template>
  <!-- Global meta-mode launcher: a fixed button + a dropdown of modes. Hidden
       in snapshot/artifact mode (no live engine to chat with). -->
  <div
    v-if="visible"
    class="meta-launcher"
    :class="`meta-launcher--${placement}`"
    :data-placement="placement"
    data-testid="meta-launcher"
  >
    <button
      class="meta-launcher__btn"
      data-testid="meta-button"
      :aria-expanded="dropdownOpen"
      title="Meta mode — edit or ask about this story / kitsoki"
      @click="toggleDropdown"
    >
      <span class="meta-launcher__spark">✦</span> Meta
      <!-- Status badges: a meta chat is working (spinner) and/or has a reply
           waiting (dot). Both can show at once — distinct modes, distinct
           states. -->
      <span
        v-if="meta.anyBusy"
        class="meta-launcher__status meta-launcher__status--busy"
        data-testid="meta-status-busy"
        title="A meta chat is working…"
        aria-label="meta chat working"
        >⟳</span
      >
      <span
        v-if="meta.anyWaiting"
        class="meta-launcher__status meta-launcher__status--ready"
        data-testid="meta-status-ready"
        title="A meta chat has a reply waiting"
        aria-label="meta chat reply waiting"
        >●</span
      >
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
        <span class="meta-launcher__item-label">
          {{ m.label }}
          <span
            v-if="status(m.key).busy"
            class="meta-launcher__status meta-launcher__status--busy"
            :data-testid="`meta-item-status-busy-${testidFor(m.key)}`"
            title="Working…"
            >⟳</span
          >
          <span
            v-else-if="status(m.key).waiting"
            class="meta-launcher__status meta-launcher__status--ready"
            :data-testid="`meta-item-status-ready-${testidFor(m.key)}`"
            title="Reply waiting"
            >●</span
          >
        </span>
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
import { isEmbedded } from "../../lib/embed.js";
import { useMetaStore } from "../../stores/meta.js";
import { useBugReportStore } from "../../stores/bugReport.js";

const props = withDefaults(
  defineProps<{
    placement?: "floating" | "topbar";
  }>(),
  {
    placement: "floating",
  }
);

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
const placement = computed(() => props.placement);
const visible = computed(() => {
  if (isSnapshot) return false;
  // The global launcher remains a bottom-right affordance in the normal web UI.
  // VS Code chat embeds render their own topbar launcher, so suppress only the
  // app-level floating instance there.
  return props.placement === "topbar" || !isEmbedded();
});

const sessionId = computed(() => {
  const p = route.params.sessionId;
  return typeof p === "string" ? p : "";
});

function isEnabled(key: string): boolean {
  return meta.modes.some((m) => m.key === key);
}

// Per-mode working/waiting status for the dropdown items, scoped to the routed
// session. A mode is "busy" while its turn streams and "waiting" once a turn
// finishes that the user hasn't viewed yet.
function status(key: string): { busy: boolean; waiting: boolean } {
  return meta.statusFor(sessionId.value, key);
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
  position: relative;
  z-index: 900;
}

.meta-launcher--floating {
  /* Bottom-right floating launcher. Deliberately NOT top-right: the session
     views put their Observe/Drive/Reload controls top-right, and a fixed
     element there would intercept clicks on them. */
  position: fixed;
  bottom: 1rem;
  right: 1rem;
}

.meta-launcher__btn {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  background: var(--k-button-bg, #1d4ed8);
  color: var(--k-button-fg, #eef2ff);
  border: 1px solid var(--k-border-focus, #2563eb);
  border-radius: 999px;
  padding: 0.32rem 0.7rem;
  font-size: 0.78rem;
  font-weight: 600;
  cursor: pointer;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.35);
  transition: background 0.12s;
}
.meta-launcher__btn:hover {
  background: var(--k-button-hover-bg, #2563eb);
}

.meta-launcher--topbar {
  display: inline-flex;
  align-items: center;
  flex: 0 0 auto;
}

.meta-launcher--topbar .meta-launcher__btn {
  height: 1.7rem;
  border-radius: 4px;
  padding: 0 0.55rem;
  background: var(--k-bg-input, #1e293b);
  border-color: var(--k-border-subtle, #334155);
  color: var(--k-fg, #e2e8f0);
  box-shadow: none;
  font-size: 0.75rem;
}

.meta-launcher--topbar .meta-launcher__btn:hover {
  background: var(--k-bg-hover, #273449);
  border-color: var(--k-border-focus, #3b82f6);
}
.meta-launcher__spark {
  font-size: 0.7rem;
}
.meta-launcher__caret {
  font-size: 0.6rem;
  opacity: 0.8;
}

/* Status badges — shared by the launcher button and the dropdown items. */
.meta-launcher__status {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 0.7rem;
  line-height: 1;
  margin-left: 0.15rem;
}
.meta-launcher__status--busy {
  color: #fbbf24; /* amber: a turn is streaming */
  animation: meta-spin 1s linear infinite;
}
.meta-launcher__status--ready {
  color: #4ade80; /* green: a reply is waiting */
  font-size: 0.55rem;
  animation: meta-pulse 1.6s ease-in-out infinite;
}
@keyframes meta-spin {
  to {
    transform: rotate(360deg);
  }
}
@keyframes meta-pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.35;
  }
}
@media (prefers-reduced-motion: reduce) {
  .meta-launcher__status--busy,
  .meta-launcher__status--ready {
    animation: none;
  }
}

.meta-launcher__menu {
  position: absolute;
  right: 0;
  /* Opens upward — the launcher sits at the bottom of the viewport. */
  bottom: 100%;
  margin-bottom: 0.35rem;
  min-width: 16rem;
  background: var(--k-bg-widget, #0d1b2a);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 6px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
  overflow: hidden;
}

.meta-launcher--topbar .meta-launcher__menu {
  top: 100%;
  bottom: auto;
  margin-top: 0.35rem;
  margin-bottom: 0;
}

.meta-launcher__item {
  display: flex;
  flex-direction: column;
  gap: 0.1rem;
  width: 100%;
  text-align: left;
  background: none;
  border: none;
  border-bottom: 1px solid var(--k-border-subtle, #16202e);
  color: var(--k-fg, #e2e8f0);
  padding: 0.5rem 0.7rem;
  cursor: pointer;
}
.meta-launcher__item:last-child {
  border-bottom: none;
}
.meta-launcher__item:hover:not(.meta-launcher__item--disabled) {
  background: var(--k-bg-hover, #15233a);
}
.meta-launcher__item--disabled {
  color: var(--k-fg-subtle, #475569);
  cursor: not-allowed;
}
.meta-launcher__item-label {
  font-size: 0.8rem;
  font-weight: 600;
}
.meta-launcher__item-hint {
  font-size: 0.68rem;
  color: var(--k-fg-muted, #64748b);
}
.meta-launcher__item:disabled {
  opacity: 0.5;
  cursor: progress;
}

.meta-launcher__divider {
  height: 1px;
  background: var(--k-border, #1e293b);
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
  background: var(--k-bg-widget, #0d1b2a);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 6px;
  padding: 0.45rem 0.6rem;
  font-size: 0.72rem;
  color: var(--k-fg, #e2e8f0);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
}

.meta-launcher--topbar .meta-launcher__toast {
  top: 100%;
  bottom: auto;
  margin-top: 0.35rem;
  margin-bottom: 0;
}
.meta-launcher__toast-link {
  background: none;
  border: none;
  color: var(--k-fg-accent, #60a5fa);
  cursor: pointer;
  font-size: 0.72rem;
  padding: 0;
}
.meta-launcher__toast-link:hover {
  text-decoration: underline;
}
</style>
