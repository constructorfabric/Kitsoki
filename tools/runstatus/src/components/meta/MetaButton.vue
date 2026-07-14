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
        v-if="bugUnavailable"
        class="meta-launcher__status meta-launcher__status--warn"
        data-testid="meta-status-bug-unavailable"
        :title="bugUnavailableMessage"
        aria-label="bug filing unavailable"
        >!</span
      >
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

      <button
        class="meta-launcher__item"
        data-testid="meta-workflow-launcher"
        title="Open dynamic workflows"
        @click="openWorkflow"
      >
        <span class="meta-launcher__item-label">Dynamic workflows</span>
        <span class="meta-launcher__item-hint">
          Create, validate, launch, and export a draft
        </span>
      </button>

      <div class="meta-launcher__divider" role="separator"></div>

      <div
        v-if="bugUnavailable"
        class="meta-launcher__warning"
        data-testid="bug-report-warning"
        role="status"
      >
        <span class="meta-launcher__warning-title">Bug filing unavailable</span>
        <span class="meta-launcher__warning-body">
          {{ bugUnavailableMessage }}
        </span>
      </div>

      <button
        class="meta-launcher__item"
        :class="{ 'meta-launcher__item--disabled': bugUnavailable }"
        data-testid="meta-report-bug"
        :title="bugReportTitle"
        :disabled="bugReportBlocked"
        @click="reportBug"
      >
        <span class="meta-launcher__item-label">Report bug</span>
        <span class="meta-launcher__item-hint">
          {{ bugReportHint }}
        </span>
      </button>
    </div>

    <div
      v-if="pointMenu"
      class="meta-context-menu"
      data-testid="bug-point-menu"
      :style="{ left: `${pointMenu.menuX}px`, top: `${pointMenu.menuY}px` }"
      role="menu"
      aria-label="Bug report context menu"
    >
      <button
        class="meta-context-menu__item"
        data-testid="bug-point-menu-report"
        type="button"
        role="menuitem"
        :disabled="bugReportBlocked"
        :title="bugReportTitle"
        @click="reportBugFromPointMenu"
      >
        <span class="meta-context-menu__label">Report bug here</span>
        <span class="meta-context-menu__hint">{{ pointMenu.context.selector }}</span>
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
        <span class="meta-launcher__toast-message" data-testid="bug-toast-path">
          Filed: {{ filedTarget }}
        </span>
        <span
          v-if="privacyStatus"
          class="meta-launcher__toast-detail"
          data-testid="bug-toast-privacy"
        >
          Privacy: {{ privacyStatus }}
        </span>
        <button
          v-if="filedTarget"
          class="meta-launcher__toast-link"
          data-testid="bug-toast-open"
          title="Open the filed bug"
          @click="openFiled"
        >
          [open]
        </button>
        <a
          v-if="bugfixHref"
          class="meta-launcher__toast-link"
          data-testid="bug-toast-bugfix"
          :href="bugfixHref"
          target="_blank"
          rel="noopener noreferrer"
          title="Open a new autonomous bugfix session for this bug"
        >
          [fix]
        </a>
        <button
          class="meta-launcher__toast-link"
          data-testid="bug-toast-dismiss"
          @click="bugReport.reset()"
        >
          ✕
        </button>
      </template>
      <template v-else-if="bugReport.status === 'error'">
        <span
          class="meta-launcher__toast-message"
          data-testid="bug-toast-error"
        >
          Bug report failed: {{ bugReport.error }}
        </span>
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
import { useWorkflowStore } from "../../stores/workflow.js";
import type { BugPlacementContext } from "../../stores/bugReport.js";
import type { BugStatusResult } from "../../data/live-source.js";

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
    key: "story.improve",
    label: "Improve run",
    hint: "Review the latest run for prompt, tool, and flow-test improvements",
    disabledHint: "Open a story first",
  },
  {
    key: "kitsoki.ask",
    label: "Kitsoki help",
    hint: "Ask about kitsoki itself (read-only)",
    disabledHint: "Unavailable",
  },
  {
    key: "kitsoki.improve",
    label: "Improve kitsoki",
    hint: "Review this run for reusable kitsoki engine improvements",
    disabledHint: "Unavailable",
  },
] as const;

const isSnapshot =
  (globalThis as typeof globalThis & { __KITSOKI_SNAPSHOT__?: unknown })
    .__KITSOKI_SNAPSHOT__ !== undefined;

const route = useRoute();
const meta = useMetaStore();
const bugReport = useBugReportStore();
const workflow = useWorkflowStore();
const source = new LiveSource("/");

const dropdownOpen = ref(false);
const pointMenu = ref<{
  context: BugPlacementContext;
  menuX: number;
  menuY: number;
} | null>(null);
const bugStatus = ref<BugStatusResult | null>(null);
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
const bugUnavailable = computed(() => bugStatus.value?.can_file === false);
const bugUnavailableMessage = computed(
  () =>
    bugStatus.value?.warning ??
    "Report bug cannot file right now. Filing bugs is critical; fix GitHub auth and reload."
);
const bugReportBlocked = computed(
  () => bugReport.status === "capturing" || bugUnavailable.value
);
const bugReportTitle = computed(() => {
  if (bugUnavailable.value) {
    return `${bugUnavailableMessage.value} ${bugStatus.value?.setup_hint ?? ""}`.trim();
  }
  return "File a bug — attaches a scrubbed network trace + session replay";
});
const bugReportHint = computed(() =>
  bugUnavailable.value
    ? "Fix GitHub auth; filing bugs is critical"
    : "Review a trace + session replay, then file an issue"
);
const filedTarget = computed(
  () => bugReport.filed?.url || bugReport.filed?.path || ""
);
const bugfixHref = computed(() => {
  const filed = bugReport.filed;
  if (!filed) return "";
  const source = filed.url || filed.path || "";
  if (!source) return "";
  const query = new URLSearchParams();
  query.set("id", filed.id);
  if (filed.path) query.set("path", filed.path);
  if (filed.url) query.set("url", filed.url);
  query.set("title", bugReport.title || filed.id);
  return `#/start/bugfix?${query.toString()}`;
});
const privacyStatus = computed(() => {
  const privacy = bugReport.filed?.privacy;
  if (!privacy) return "";
  const message = privacy.message || privacy.status;
  return privacy.follow_up_path
    ? `${message}; follow-up ${privacy.follow_up_path}`
    : message;
});

async function refreshBugStatus(): Promise<void> {
  if (isSnapshot) return;
  try {
    bugStatus.value = await source.bugStatus();
  } catch {
    // Older or mocked servers may not expose status yet. Preserve the existing
    // report-bug behavior instead of failing the whole Meta launcher.
    bugStatus.value = null;
  }
}

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
  pointMenu.value = null;
  if (blockUnavailableBugReport()) return;
  await bugReport.trigger({
    source,
    defaultTitle: "Bug report",
    severity: "med",
    traceRef: sessionId.value || undefined,
  });
}

async function reportBugAtContext(ctx?: BugPlacementContext): Promise<void> {
  if (!ctx || bugReport.status === "capturing") return;
  pointMenu.value = null;
  dropdownOpen.value = false;
  if (blockUnavailableBugReport()) return;
  await bugReport.trigger({
    source,
    defaultTitle: "Bug report at clicked location",
    severity: "med",
    traceRef: sessionId.value || undefined,
    placement: ctx,
  });
}

async function reportBugFromPointMenu(): Promise<void> {
  await reportBugAtContext(pointMenu.value?.context);
}

function reportBugAt(e: MouseEvent): void {
  if (!e.altKey || !visible.value || bugReport.status === "capturing") return;
  const target = e.target as HTMLElement | null;
  if (!target || shouldIgnorePointReport(target)) return;
  e.preventDefault();
  e.stopPropagation();
  void reportBugAtContext(placementContext(e, target));
}

function openPointMenu(e: MouseEvent): void {
  if (!e.altKey || !visible.value || bugReport.status === "capturing") return;
  const target = e.target as HTMLElement | null;
  if (!target || shouldIgnorePointReport(target)) return;
  e.preventDefault();
  e.stopPropagation();
  dropdownOpen.value = false;
  pointMenu.value = {
    context: placementContext(e, target),
    ...pointMenuPosition(e),
  };
}

function openWorkflow(): void {
  dropdownOpen.value = false;
  pointMenu.value = null;
  workflow.openPanel();
}

function blockUnavailableBugReport(): boolean {
  if (!bugUnavailable.value) return false;
  bugReport.error = bugUnavailableMessage.value;
  bugReport.status = "error";
  return true;
}

// The toast is for capture-in-progress and the post-submit result. While the
// operator reviews (reviewing/submitting) the modal owns the surface.
const showToast = computed(
  () =>
    bugReport.status === "capturing" ||
    bugReport.status === "filed" ||
    bugReport.status === "error"
);

function openFiled(): void {
  if (!filedTarget.value) return;
  window.open(filedTarget.value, "_blank");
}

function shouldIgnorePointReport(target: HTMLElement): boolean {
  if (
    target.closest(
      ".meta-launcher, .br-backdrop, input, textarea, select, [contenteditable='true'], [role='textbox']"
    )
  ) {
    return true;
  }
  return false;
}

function placementContext(e: MouseEvent, target: HTMLElement): BugPlacementContext {
  return {
    x: e.clientX,
    y: e.clientY,
    selector: describeTarget(target),
    text: visibleText(target),
    route: `${window.location.pathname}${window.location.hash}`,
  };
}

function pointMenuPosition(e: MouseEvent): { menuX: number; menuY: number } {
  const margin = 8;
  const width = 260;
  const height = 64;
  return {
    menuX: Math.max(margin, Math.min(e.clientX, window.innerWidth - width - margin)),
    menuY: Math.max(margin, Math.min(e.clientY, window.innerHeight - height - margin)),
  };
}

function visibleText(target: HTMLElement): string | undefined {
  const text = (target.innerText || target.textContent || "")
    .replace(/\s+/g, " ")
    .trim();
  if (!text) return undefined;
  return text.length > 140 ? `${text.slice(0, 137)}...` : text;
}

function describeTarget(target: HTMLElement): string {
  const testId = target.getAttribute("data-testid");
  if (testId) return `[data-testid="${testId}"]`;
  const aria = target.getAttribute("aria-label");
  if (aria) return `${target.tagName.toLowerCase()}[aria-label="${cssEscape(aria)}"]`;
  const id = target.id ? `#${cssEscape(target.id)}` : "";
  if (id) return `${target.tagName.toLowerCase()}${id}`;
  const path: string[] = [];
  let el: HTMLElement | null = target;
  while (el && el !== document.body && path.length < 4) {
    path.unshift(describePart(el));
    el = el.parentElement;
  }
  return path.join(" > ");
}

function describePart(el: HTMLElement): string {
  const testId = el.getAttribute("data-testid");
  if (testId) return `[data-testid="${testId}"]`;
  const cls = Array.from(el.classList)
    .filter((c) => c && !c.startsWith("v-"))
    .slice(0, 2)
    .map((c) => `.${cssEscape(c)}`)
    .join("");
  return `${el.tagName.toLowerCase()}${cls}`;
}

function cssEscape(s: string): string {
  const esc = (globalThis as typeof globalThis & { CSS?: { escape?: (v: string) => string } }).CSS?.escape;
  return esc ? esc(s) : s.replace(/["\\]/g, "\\$&");
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
  if (el && el.closest(".meta-launcher, .meta-context-menu")) return;
  dropdownOpen.value = false;
  pointMenu.value = null;
}
onMounted(() => {
  void refreshBugStatus();
  document.addEventListener("click", onDocClick);
  document.addEventListener("click", reportBugAt, true);
  document.addEventListener("contextmenu", openPointMenu, true);
});
onUnmounted(() => {
  document.removeEventListener("click", onDocClick);
  document.removeEventListener("click", reportBugAt, true);
  document.removeEventListener("contextmenu", openPointMenu, true);
});
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
.meta-launcher__status--warn {
  color: var(--k-warning, #f59e0b);
  font-size: 0.72rem;
  font-weight: 800;
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

.meta-context-menu {
  position: fixed;
  z-index: 1200;
  min-width: 16rem;
  max-width: min(22rem, calc(100vw - 1rem));
  background: var(--k-bg-widget, #0d1b2a);
  border: 1px solid var(--k-border-focus, #2563eb);
  border-radius: 6px;
  box-shadow: 0 14px 36px rgba(0, 0, 0, 0.55);
  overflow: hidden;
}

.meta-context-menu__item {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
  width: 100%;
  text-align: left;
  background: none;
  border: none;
  color: var(--k-fg, #e2e8f0);
  padding: 0.6rem 0.75rem;
  cursor: pointer;
}

.meta-context-menu__item:hover:not(:disabled) {
  background: var(--k-bg-hover, #15233a);
}

.meta-context-menu__item:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.meta-context-menu__label {
  font-size: 0.82rem;
  font-weight: 700;
}

.meta-context-menu__hint {
  max-width: 20rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--k-fg-muted, #64748b);
  font-size: 0.68rem;
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
  cursor: not-allowed;
}

.meta-launcher__divider {
  height: 1px;
  background: var(--k-border, #1e293b);
}

.meta-launcher__warning {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
  padding: 0.55rem 0.7rem;
  border-bottom: 1px solid var(--k-border-subtle, #16202e);
  background: color-mix(in srgb, var(--k-warning, #f59e0b) 12%, transparent);
}
.meta-launcher__warning-title {
  color: var(--k-warning, #f59e0b);
  font-size: 0.72rem;
  font-weight: 700;
}
.meta-launcher__warning-body {
  color: var(--k-fg, #cbd5e1);
  font-size: 0.68rem;
  line-height: 1.35;
}

.meta-launcher__toast {
  position: absolute;
  right: 0;
  bottom: 100%;
  margin-bottom: 0.35rem;
  width: max-content;
  max-width: min(28rem, calc(100vw - 2rem));
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  gap: 0.4rem;
  background: var(--k-bg-widget, #0d1b2a);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 6px;
  padding: 0.45rem 0.6rem;
  font-size: 0.72rem;
  color: var(--k-fg, #e2e8f0);
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
}

.meta-launcher__toast-message {
  min-width: 0;
  line-height: 1.35;
  overflow-wrap: anywhere;
}

.meta-launcher__toast-detail {
  color: var(--k-fg-muted, #94a3b8);
  overflow-wrap: anywhere;
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
