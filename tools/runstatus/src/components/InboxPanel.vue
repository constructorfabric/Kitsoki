<template>
  <!-- Global inbox panel: opens on badge click. A floating card anchored above
       the chrome launchers (bottom-right), reusing the Meta launcher placement
       rationale. Closes on backdrop click or Esc. -->
  <Teleport to="body">
    <div
      v-if="inbox.open"
      class="inbox-panel__backdrop"
      @click.self="inbox.close()"
    >
      <div class="inbox-panel" data-testid="inbox-panel">
        <div class="inbox-panel__header">
          <span class="inbox-panel__title">
            Inbox
            <span v-if="inbox.unread > 0" class="inbox-panel__count">{{ inbox.unread }}</span>
          </span>
          <button
            class="inbox-panel__sync"
            data-testid="inbox-sync-github"
            title="Refresh assigned GitHub issues and requested PR reviews"
            :disabled="!currentSessionId || inbox.githubSyncing"
            @click="onSyncGitHub"
          >{{ inbox.githubSyncing ? "Syncing…" : "Sync GitHub" }}</button>
          <button
            class="inbox-panel__close"
            data-testid="inbox-close"
            title="Close (Esc)"
            @click="inbox.close()"
          >✕</button>
        </div>

        <div class="inbox-panel__body">
          <div
            v-if="inbox.githubSyncError"
            class="inbox-panel__error"
            data-testid="inbox-sync-error"
          >
            {{ inbox.githubSyncError }}
          </div>
          <div
            v-else-if="inbox.githubSyncLast"
            class="inbox-panel__sync-status"
            data-testid="inbox-sync-status"
          >
            GitHub sync: {{ inbox.githubSyncLast.inserted }} new, {{ inbox.githubSyncLast.skipped }} existing
          </div>
          <section
            v-if="panelActiveWorkCount > 0"
            class="work-section"
            aria-label="Active work"
          >
            <div class="work-section__header">
              <span>Active work</span>
              <span class="work-section__count">{{ panelActiveWorkCount }}</span>
            </div>
            <button
              v-for="proposal in proposalApprovalItems"
              :key="`proposal:${proposal.id}`"
              class="work-item work-item--proposal"
              data-testid="work-item"
              @click="onProposalItem(proposal.id)"
            >
              <span class="work-item__kind">{{ proposal.kind === "write_mode" ? "approval" : "proposal" }}</span>
              <span class="work-item__main">
                <span class="work-item__title">{{ proposal.title || "(untitled)" }}</span>
                <span v-if="proposal.detail" class="work-item__body">{{ proposal.detail }}</span>
                <span class="work-item__meta">
                  <span>{{ proposal.kind }}</span>
                  <span class="work-item__action">review</span>
                </span>
              </span>
            </button>
            <button
              v-for="item in inbox.workItems"
              :key="workKey(item)"
              class="work-item"
              :class="`work-item--${item.kind}`"
              data-testid="work-item"
              @click="onWorkItem(item)"
            >
              <span class="work-item__kind">{{ workKind(item) }}</span>
              <span class="work-item__main">
                <span class="work-item__title">{{ item.title || "(untitled)" }}</span>
                <span v-if="item.body" class="work-item__body">{{ item.body }}</span>
                <span v-if="item.origin_url" class="work-item__origin">{{ item.origin_url }}</span>
                <span v-if="workContext(item)" class="work-item__context">{{ workContext(item) }}</span>
                <span class="work-item__meta">
                  <span>{{ item.status || item.kind }}</span>
                  <span v-if="item.updated_at">{{ relativeTime(item.updated_at) }}</span>
                  <span class="work-item__action">{{ workAction(item) }}</span>
                </span>
              </span>
            </button>
            <button
              v-for="proposal in proposalStructureItems"
              :key="`proposal:${proposal.id}`"
              class="work-item work-item--proposal"
              data-testid="work-item"
              @click="onProposalItem(proposal.id)"
            >
              <span class="work-item__kind">proposal</span>
              <span class="work-item__main">
                <span class="work-item__title">{{ proposal.title || "(untitled)" }}</span>
                <span v-if="proposal.detail" class="work-item__body">{{ proposal.detail }}</span>
                <span class="work-item__meta">
                  <span>{{ proposal.kind }}</span>
                  <span class="work-item__action">review</span>
                </span>
              </span>
            </button>
          </section>

          <p
            v-if="panelActiveWorkCount === 0 && inbox.notifications.length === 0"
            class="inbox-panel__empty"
          >
            No notifications yet — a background turn that finishes will land here.
          </p>
          <div
            v-if="inbox.notifications.length > 0"
            class="inbox-panel__subhead"
          >
            Notifications
          </div>
          <div
            v-for="n in inbox.notifications"
            :key="n.ID"
            class="inbox-item"
            :class="{ 'inbox-item--unread': !n.ReadAt }"
            data-testid="inbox-item"
          >
            <span
              class="inbox-item__glyph"
              :style="{ color: severityColor(n.Severity) }"
              :title="n.Severity"
            >{{ severityGlyph(n.Severity) }}</span>
            <div class="inbox-item__main">
              <div class="inbox-item__title">{{ n.Title || "(untitled)" }}</div>
              <div v-if="n.Body" class="inbox-item__body">{{ n.Body }}</div>
              <div class="inbox-item__meta">
                <span class="inbox-item__time">{{ relativeTime(n.CreatedAt) }}</span>
                <button
                  class="inbox-item__jump"
                  data-testid="inbox-jump"
                  title="Jump to where this happened"
                  @click="onJump(n)"
                >jump →</button>
                <button
                  class="inbox-item__dismiss"
                  data-testid="inbox-dismiss"
                  title="Dismiss"
                  @click="onDismiss(n)"
                >dismiss</button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { LiveSource } from "../data/live-source.js";
import type { Notification, WorkItem } from "../data/live-source.js";
import { useInboxStore } from "../stores/inbox.js";
import { useOperatorQuestionStore } from "../stores/operatorQuestions.js";
import { useProposalsStore } from "../stores/proposals.js";
import { severityGlyph, severityColor, relativeTime } from "../lib/severity.js";
import { jumpToNotification } from "../lib/inbox-jump.js";

const inbox = useInboxStore();
const operatorQuestions = useOperatorQuestionStore();
const proposals = useProposalsStore();
const router = useRouter();
const route = useRoute();
const source = new LiveSource("/");

const panelActiveWorkCount = computed(
  () => inbox.activeWorkCount + proposals.count
);
const proposalApprovalItems = computed(() =>
  proposals.queue.filter((proposal) => proposal.kind === "write_mode")
);
const proposalStructureItems = computed(() =>
  proposals.queue.filter((proposal) => proposal.kind !== "write_mode")
);

const currentSessionId = computed(() => {
  const raw = route.params.sessionId;
  return typeof raw === "string" ? raw : "";
});

watch(
  () => route.query.inbox,
  (raw) => {
    if (!opensInbox(raw)) return;
    inbox.openPanel();
    void inbox.refreshWork(source);
  },
  { immediate: true }
);

function opensInbox(raw: unknown): boolean {
  const value = Array.isArray(raw) ? raw[0] : raw;
  return value === "1" || value === "true" || value === "open" || value === "work";
}

async function onJump(n: Notification): Promise<void> {
  await jumpToNotification(router, source, n);
}

function onDismiss(n: Notification): void {
  void inbox.dismiss(source, n.SessionID, n.ID);
}

function onSyncGitHub(): void {
  if (!currentSessionId.value) return;
  void inbox.syncGitHub(source, currentSessionId.value);
}

function onProposalItem(proposalID: string): void {
  const index = proposals.queue.findIndex((p) => p.id === proposalID);
  if (index < 0) return;
  const proposal = proposals.queue[index];
  if (!proposal) return;
  operatorQuestions.onFrame({
    session_id: "",
    question_id: proposal.id,
    questions: [
      {
        question: proposal.detail ? `${proposal.title}\n${proposal.detail}` : proposal.title,
        header: proposal.kind === "write_mode" ? "May I edit?" : "Capture as structure?",
        options: [{ label: "accept" }, { label: "refine" }, { label: "dismiss" }],
      },
    ],
  });
  proposals.queue.splice(index, 1);
  inbox.close();
}

async function onWorkItem(item: WorkItem): Promise<void> {
  if (item.kind === "operator_question" && item.question_id && item.questions) {
    operatorQuestions.onFrame({
      session_id: item.session_id,
      question_id: item.question_id,
      questions: item.questions,
    });
    inbox.close();
    return;
  }
  if (isNotificationBackedWork(item)) {
    await jumpToNotification(router, source, notificationFromWork(item));
    return;
  }
  const sid = item.reacquire_session_id || item.session_id;
  if (!sid) return;
  inbox.close();
  await router.push(workRoute(item, sid));
}

function notificationFromWork(item: WorkItem): Notification {
  return {
    ID: item.notification_id || "",
    SessionID: item.session_id,
    CreatedAt: item.created_at || new Date().toISOString(),
    Severity: item.severity || "info",
    Title: item.title || "",
    Body: item.body || "",
    TeleportState: item.teleport_state || "",
    TeleportSlots: item.teleport_slots || null,
    TeleportProposalID: "",
    TeleportJobID: item.teleport_job_id || "",
    OriginKind: item.origin_kind || "work",
    OriginRef: item.origin_ref || item.job_id || item.notification_id || "",
    OriginURL: item.origin_url || null,
    ReadAt: item.read_at ?? null,
  };
}

function workKey(item: WorkItem): string {
  return `${item.kind}:${item.notification_id || item.job_id || item.drive_id || item.chat_id || item.question_id || item.proposal_id || item.session_id}`;
}

function workKind(item: WorkItem): string {
  if (item.kind === "operator_question") return "question";
  if (item.kind === "mining_proposal") return "proposal";
  if (item.kind === "job") return "job";
  if (item.kind === "pending_drive") {
    return item.status === "dispatching" ? "dispatching" : "queued";
  }
  if (item.kind === "failed_drive") return "failed";
  if (item.kind === "backgrounded_chat") return "chat";
  if (item.kind === "notification") return item.severity || "note";
  return item.kind;
}

function workContext(item: WorkItem): string {
  if (item.kind === "mining_proposal") {
    const parts: string[] = [];
    if (item.proposal_kind) parts.push(item.proposal_kind);
    if (item.proposal_target) parts.push(item.proposal_target);
    if (item.rung) parts.push(`rung ${item.rung}`);
    if (item.draft_path) parts.push(item.draft_path);
    return parts.join(" | ");
  }
  if (item.kind !== "pending_drive" && item.kind !== "backgrounded_chat") return "";

  const parts: string[] = [];
  if (item.chat_id) parts.push(`chat ${item.chat_id}`);
  if (item.drive_id) parts.push(`drive ${item.drive_id}`);
  if (item.actor) parts.push(item.actor);
  if (item.thread) parts.push(item.thread);
  if (item.tmux_session) parts.push(`tmux ${item.tmux_session}`);
  if (item.tmux_host) parts.push(item.tmux_host);
  return parts.join(" | ");
}

function workAction(item: WorkItem): string {
  if (item.kind === "operator_question") return "answer";
  if (item.kind === "mining_proposal") return "review";
  if (isNotificationBackedWork(item)) return "jump";
  if (item.reacquire_tool === "chat.show" || item.chat_id) return "open context";
  return "open session";
}

function workRoute(item: WorkItem, sessionId: string): string {
  if (item.reacquire_tool === "chat.show" || item.chat_id) {
    const chat = item.chat_id ? `?chat=${encodeURIComponent(item.chat_id)}` : "";
    return `/s/${sessionId}/chat${chat}`;
  }
  return `/s/${sessionId}`;
}

function isNotificationBackedWork(item: WorkItem): boolean {
  return !!item.notification_id && (item.kind === "notification" || item.reacquire_tool === "notification");
}

function onKeydown(e: KeyboardEvent): void {
  if (e.key === "Escape" && inbox.open) inbox.close();
}
onMounted(() => window.addEventListener("keydown", onKeydown));
onUnmounted(() => window.removeEventListener("keydown", onKeydown));
</script>

<style scoped>
.inbox-panel__backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.4);
  z-index: 1000;
}

.inbox-panel {
  position: fixed;
  right: 1rem;
  bottom: 3.6rem; /* above the chrome launchers */
  width: 22rem;
  max-width: calc(100vw - 2rem);
  max-height: 70vh;
  display: flex;
  flex-direction: column;
  background: var(--k-bg-widget, #0d1b2a);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 8px;
  box-shadow: 0 10px 30px rgba(0, 0, 0, 0.55);
  color: var(--k-fg, #e2e8f0);
  overflow: hidden;
}

.inbox-panel__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.5rem 0.7rem;
  border-bottom: 1px solid var(--k-border, #1e293b);
  flex-shrink: 0;
}
.inbox-panel__title {
  font-size: 0.85rem;
  font-weight: 600;
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
}
.inbox-panel__count {
  background: var(--k-button-bg, #1d4ed8);
  color: var(--k-button-fg, #eef2ff);
  border-radius: 999px;
  font-size: 0.65rem;
  padding: 0.05rem 0.4rem;
}
.inbox-panel__close {
  background: none;
  border: none;
  color: var(--k-fg-muted, #64748b);
  cursor: pointer;
  font-size: 0.9rem;
}
.inbox-panel__sync {
  margin-left: auto;
  margin-right: 0.45rem;
  border: 1px solid var(--k-border, #334155);
  background: var(--k-bg-input, #101826);
  color: var(--k-fg, #e2e8f0);
  border-radius: 6px;
  padding: 0.2rem 0.45rem;
  font-size: 0.7rem;
  cursor: pointer;
}
.inbox-panel__sync:disabled {
  cursor: default;
  opacity: 0.45;
}
.inbox-panel__close:hover {
  color: var(--k-fg, #e2e8f0);
}

.inbox-panel__body {
  flex: 1;
  overflow: auto;
}
.inbox-panel__error {
  margin: 0.55rem 0.7rem 0;
  color: var(--k-error-fg, #fecaca);
  font-size: 0.75rem;
}
.inbox-panel__sync-status {
  margin: 0.55rem 0.7rem 0;
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.75rem;
}
.work-section {
  border-bottom: 1px solid var(--k-border, #16202e);
}
.work-section__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.5rem 0.7rem 0.25rem;
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.7rem;
  font-weight: 700;
  text-transform: uppercase;
}
.work-section__count {
  color: var(--k-fg, #e2e8f0);
  font-weight: 600;
}
.work-item {
  width: 100%;
  display: flex;
  align-items: flex-start;
  gap: 0.55rem;
  padding: 0.55rem 0.7rem;
  border: 0;
  border-top: 1px solid var(--k-border, #16202e);
  background: transparent;
  color: inherit;
  cursor: pointer;
  text-align: left;
}
.work-item:hover {
  background: var(--k-bg-hover, #16263a);
}
.work-item__kind {
  min-width: 3.7rem;
  color: var(--k-fg-accent, #60a5fa);
  font-size: 0.68rem;
  font-weight: 700;
  text-transform: uppercase;
}
.work-item--notification .work-item__kind {
  color: var(--k-warning, #fb923c);
}
.work-item__main {
  min-width: 0;
  display: grid;
  gap: 0.15rem;
}
.work-item__title {
  font-size: 0.78rem;
  font-weight: 650;
  overflow-wrap: anywhere;
}
.work-item__body,
.work-item__origin,
.work-item__context {
  font-size: 0.7rem;
  color: var(--k-fg-muted, #94a3b8);
  overflow-wrap: anywhere;
}
.work-item__body {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.work-item__origin {
  color: var(--k-fg-accent, #60a5fa);
}
.work-item__context {
  color: var(--k-fg-muted, #94a3b8);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
}
.work-item__meta {
  display: inline-flex;
  flex-wrap: wrap;
  gap: 0.45rem;
  color: var(--k-fg-muted, #64748b);
  font-size: 0.68rem;
}
.work-item__action {
  color: var(--k-fg-accent, #60a5fa);
  font-weight: 650;
}
.inbox-panel__empty {
  color: var(--k-fg-subtle, #475569);
  font-size: 0.78rem;
  font-style: italic;
  padding: 1rem;
}
.inbox-panel__subhead {
  padding: 0.5rem 0.7rem 0.25rem;
  color: var(--k-fg-muted, #94a3b8);
  font-size: 0.7rem;
  font-weight: 700;
  text-transform: uppercase;
}

.inbox-item {
  display: flex;
  gap: 0.55rem;
  padding: 0.6rem 0.7rem;
  border-bottom: 1px solid var(--k-border, #16202e);
}
.inbox-item--unread {
  background: var(--k-bg-selection, #0f2238);
}
.inbox-item__glyph {
  font-size: 0.95rem;
  line-height: 1.2;
  flex-shrink: 0;
}
.inbox-item__main {
  flex: 1;
  min-width: 0;
}
.inbox-item__title {
  font-size: 0.8rem;
  font-weight: 600;
}
.inbox-item__body {
  font-size: 0.74rem;
  color: var(--k-fg-muted, #94a3b8);
  margin-top: 0.15rem;
  overflow-wrap: anywhere;
}
.inbox-item__meta {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  margin-top: 0.3rem;
  font-size: 0.7rem;
}
.inbox-item__time {
  color: var(--k-fg-muted, #64748b);
}
.inbox-item__jump {
  background: none;
  border: none;
  color: var(--k-fg-accent, #60a5fa);
  cursor: pointer;
  font-size: 0.7rem;
  padding: 0;
}
.inbox-item__jump:hover {
  text-decoration: underline;
}
.inbox-item__dismiss {
  background: none;
  border: none;
  color: var(--k-fg-muted, #64748b);
  cursor: pointer;
  font-size: 0.7rem;
  padding: 0;
  margin-left: auto;
}
.inbox-item__dismiss:hover {
  color: var(--k-fg, #cbd5e1);
}
</style>
