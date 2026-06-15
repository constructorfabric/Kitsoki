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
            class="inbox-panel__close"
            data-testid="inbox-close"
            title="Close (Esc)"
            @click="inbox.close()"
          >✕</button>
        </div>

        <div class="inbox-panel__body">
          <p v-if="inbox.notifications.length === 0" class="inbox-panel__empty">
            No notifications yet — a background turn that finishes will land here.
          </p>
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
import { onMounted, onUnmounted } from "vue";
import { useRouter } from "vue-router";
import { LiveSource } from "../data/live-source.js";
import type { Notification } from "../data/live-source.js";
import { useInboxStore } from "../stores/inbox.js";
import { severityGlyph, severityColor, relativeTime } from "../lib/severity.js";
import { jumpToNotification } from "../lib/inbox-jump.js";

const inbox = useInboxStore();
const router = useRouter();
const source = new LiveSource("/");

async function onJump(n: Notification): Promise<void> {
  await jumpToNotification(router, source, n);
}

function onDismiss(n: Notification): void {
  void inbox.dismiss(source, n.SessionID, n.ID);
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
.inbox-panel__close:hover {
  color: var(--k-fg, #e2e8f0);
}

.inbox-panel__body {
  flex: 1;
  overflow: auto;
}
.inbox-panel__empty {
  color: var(--k-fg-subtle, #475569);
  font-size: 0.78rem;
  font-style: italic;
  padding: 1rem;
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
