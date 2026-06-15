<template>
  <div
    class="confidence-bar"
    data-testid="confidence-bar"
    :data-confidence="confidence"
    :data-threshold="threshold"
  >
    <div class="confidence-bar__track">
      <!-- Filled portion -->
      <div
        class="confidence-bar__fill"
        :class="passing ? 'confidence-bar__fill--pass' : 'confidence-bar__fill--fail'"
        :style="{ width: fillPct + '%' }"
      />
      <!-- Threshold tick -->
      <div
        class="confidence-bar__tick"
        :style="{ left: tickPct + '%' }"
        title="Threshold"
      />
    </div>
    <span class="confidence-bar__label">
      {{ fmtConf(confidence) }}<span class="confidence-bar__thr"> (thr {{ fmtConf(threshold) }})</span>
    </span>
  </div>
</template>

<script setup lang="ts">
import { computed } from "vue";

const props = withDefaults(
  defineProps<{
    confidence: number;
    threshold?: number;
    label?: string;
  }>(),
  { threshold: 0.8 }
);

const passing = computed(() => props.confidence >= props.threshold);
const fillPct = computed(() => Math.min(100, Math.max(0, props.confidence * 100)));
const tickPct = computed(() => Math.min(100, Math.max(0, props.threshold * 100)));

function fmtConf(v: number): string {
  return v.toFixed(2);
}
</script>

<style scoped>
.confidence-bar {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  min-width: 0;
}

.confidence-bar__track {
  position: relative;
  flex: 1;
  height: 6px;
  background: var(--k-bg-input, #1e293b);
  border-radius: 3px;
  overflow: visible;
  min-width: 6rem;
}

.confidence-bar__fill {
  height: 100%;
  border-radius: 3px;
  transition: width 0.2s ease;
}

.confidence-bar__fill--pass {
  background: var(--k-success, #22c55e);
}

.confidence-bar__fill--fail {
  background: var(--k-error, #ef4444);
}

.confidence-bar__tick {
  position: absolute;
  top: -2px;
  bottom: -2px;
  width: 2px;
  background: var(--k-fg-muted, #94a3b8);
  border-radius: 1px;
  transform: translateX(-50%);
}

.confidence-bar__label {
  font-family: ui-monospace, monospace;
  font-size: 0.7rem;
  color: var(--k-fg-muted, #94a3b8);
  white-space: nowrap;
  flex-shrink: 0;
}

.confidence-bar__thr {
  color: var(--k-fg-subtle, #64748b);
}
</style>
