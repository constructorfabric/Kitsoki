<template>
  <!-- Persistent help launcher: replays the guided tour. Hidden in
       snapshot/artifact mode (no live server to drive). Sits bottom-right,
       stacked ABOVE the Meta launcher so the two never overlap. -->
  <button
    v-if="!isSnapshot"
    class="tour-help"
    data-testid="tour-help"
    title="Replay the guided tour"
    @click="tour.start(true)"
  >?</button>
</template>

<script setup lang="ts">
import { useTourStore } from "../../stores/tour.js";

const tour = useTourStore();

const isSnapshot =
  (globalThis as typeof globalThis & { __KITSOKI_SNAPSHOT__?: unknown })
    .__KITSOKI_SNAPSHOT__ !== undefined;
</script>

<style scoped>
.tour-help {
  position: fixed;
  /* Above the Meta launcher (bottom: 1rem, ~32px tall) — clear of its click box. */
  bottom: 3.4rem;
  right: 1rem;
  z-index: 900;
  width: 1.85rem;
  height: 1.85rem;
  border-radius: 999px;
  background: #0d1b2a;
  color: #93c5fd;
  border: 1px solid #1e3a5f;
  font-size: 0.9rem;
  font-weight: 700;
  cursor: pointer;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.35);
  transition: background 0.12s, color 0.12s;
}
.tour-help:hover {
  background: #15233a;
  color: #bfdbfe;
}
</style>
