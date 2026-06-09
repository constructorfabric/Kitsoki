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
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { useRoute } from "vue-router";
import { LiveSource } from "../../data/live-source.js";
import { useMetaStore } from "../../stores/meta.js";

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
</style>
