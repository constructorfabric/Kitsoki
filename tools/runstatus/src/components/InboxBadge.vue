<template>
  <!-- Global inbox badge: a fixed launcher in the chrome, alongside the Meta
       button. Shows the active-work count, falling back to unread count while
       work refreshes; turns the severity color when notifications or active
       work need attention. -->
  <div v-if="!isSnapshot" class="inbox-badge-host">
    <button
      class="inbox-badge"
      data-testid="inbox-badge"
      :class="{ 'inbox-badge--attention': chromeNeedsAttention }"
      :data-needs-attention="chromeNeedsAttention ? 'true' : 'false'"
      :data-unread="inbox.unread"
      :data-active-work="inbox.activeWorkCount"
      :data-proposals="proposals.count"
      :title="`Inbox — ${chromeCount} active`"
      :aria-label="`Inbox, ${chromeCount} active`"
      @click="inbox.toggle()"
    >
      <span class="inbox-badge__bell">🔔</span>
      <span
        v-if="chromeCount > 0"
        class="inbox-badge__count"
        data-testid="inbox-badge-count"
      >{{ chromeCount }}</span>
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed } from "vue";
import { useInboxStore } from "../stores/inbox.js";
import { useProposalsStore } from "../stores/proposals.js";

const isSnapshot =
  (globalThis as typeof globalThis & { __KITSOKI_SNAPSHOT__?: unknown })
    .__KITSOKI_SNAPSHOT__ !== undefined;

const inbox = useInboxStore();
const proposals = useProposalsStore();
const chromeCount = computed(() => inbox.chromeCount + proposals.count);
const chromeNeedsAttention = computed(
  () => inbox.chromeNeedsAttention || proposals.attention
);
</script>

<style scoped>
.inbox-badge-host {
  /* Bottom-right launcher stack, sitting just above the Meta button. */
  position: fixed;
  bottom: 3.5rem;
  right: 1rem;
  z-index: 900;
}

.inbox-badge {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 2.2rem;
  height: 2.2rem;
  background: var(--k-bg-input, #1e293b);
  border: 1px solid var(--k-border, #334155);
  border-radius: 999px;
  cursor: pointer;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.35);
  transition: background 0.12s, border-color 0.12s;
}
.inbox-badge:hover {
  background: var(--k-bg-hover, #273449);
}
.inbox-badge--attention {
  border-color: var(--k-warning, #fb923c);
  box-shadow: 0 0 0 2px rgba(251, 146, 60, 0.35);
}
.inbox-badge__bell {
  font-size: 1rem;
  line-height: 1;
}
.inbox-badge__count {
  position: absolute;
  top: -0.3rem;
  right: -0.3rem;
  min-width: 1.05rem;
  height: 1.05rem;
  padding: 0 0.25rem;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: var(--k-button-bg, #1d4ed8);
  color: var(--k-button-fg, #eef2ff);
  border-radius: 999px;
  font-size: 0.62rem;
  font-weight: 700;
}
.inbox-badge--attention .inbox-badge__count {
  background: var(--k-warning, #fb923c);
  color: #1a1207;
}
</style>
