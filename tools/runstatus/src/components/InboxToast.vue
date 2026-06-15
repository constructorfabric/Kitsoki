<template>
  <!-- Transient toast on an SSE push (success / action_required only). Auto
       dismisses; click = jump to origin (same as a panel item). -->
  <Teleport to="body">
    <transition name="inbox-toast-fade">
      <button
        v-if="inbox.toast"
        class="inbox-toast"
        data-testid="inbox-toast"
        @click="onClick"
      >
        <span
          class="inbox-toast__glyph"
          :style="{ color: severityColor(inbox.toast.Severity) }"
        >{{ severityGlyph(inbox.toast.Severity) }}</span>
        <span class="inbox-toast__text">
          <span class="inbox-toast__title">{{ inbox.toast.Title || "Notification" }}</span>
          <span v-if="inbox.toast.Body" class="inbox-toast__body">{{ inbox.toast.Body }}</span>
        </span>
        <span class="inbox-toast__cta">jump →</span>
      </button>
    </transition>
  </Teleport>
</template>

<script setup lang="ts">
import { watch, onUnmounted } from "vue";
import { useRouter } from "vue-router";
import { LiveSource } from "../data/live-source.js";
import { useInboxStore } from "../stores/inbox.js";
import { severityGlyph, severityColor } from "../lib/severity.js";
import { jumpToNotification } from "../lib/inbox-jump.js";

const inbox = useInboxStore();
const router = useRouter();
const source = new LiveSource("/");

const AUTO_DISMISS_MS = 6000;
let timer: ReturnType<typeof setTimeout> | null = null;

// Reset the auto-dismiss timer each time a new toast appears.
watch(
  () => inbox.toast?.ID,
  (id) => {
    if (timer) clearTimeout(timer);
    if (id) {
      timer = setTimeout(() => inbox.clearToast(), AUTO_DISMISS_MS);
    }
  }
);

async function onClick(): Promise<void> {
  const n = inbox.toast;
  if (!n) return;
  await jumpToNotification(router, source, n);
}

onUnmounted(() => {
  if (timer) clearTimeout(timer);
});
</script>

<style scoped>
.inbox-toast {
  position: fixed;
  bottom: 4.5rem;
  left: 50%;
  transform: translateX(-50%);
  z-index: 1100;
  display: flex;
  align-items: center;
  gap: 0.55rem;
  max-width: min(28rem, calc(100vw - 2rem));
  text-align: left;
  background: var(--k-bg-widget, #0d1b2a);
  border: 1px solid var(--k-border, #1e3a5f);
  border-radius: 8px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
  color: var(--k-fg, #e2e8f0);
  padding: 0.6rem 0.8rem;
  cursor: pointer;
  font: inherit;
}
.inbox-toast:hover {
  border-color: var(--k-border-focus, #2563eb);
}
.inbox-toast__glyph {
  font-size: 1.05rem;
  flex-shrink: 0;
}
.inbox-toast__text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.inbox-toast__title {
  font-size: 0.8rem;
  font-weight: 600;
}
.inbox-toast__body {
  font-size: 0.72rem;
  color: var(--k-fg-muted, #94a3b8);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 22rem;
}
.inbox-toast__cta {
  margin-left: auto;
  color: var(--k-fg-accent, #60a5fa);
  font-size: 0.72rem;
  font-weight: 600;
  flex-shrink: 0;
}

.inbox-toast-fade-enter-active,
.inbox-toast-fade-leave-active {
  transition: opacity 0.18s ease, transform 0.18s ease;
}
.inbox-toast-fade-enter-from,
.inbox-toast-fade-leave-to {
  opacity: 0;
  transform: translateX(-50%) translateY(0.5rem);
}
</style>
