<template>
  <div class="routing-detail" data-testid="routing-detail">
    <template v-if="hasData">
      <!-- Tier / routed_by badge -->
      <div v-if="routedBy" class="routing-detail__row">
        <span class="routing-detail__key">Routed By</span>
        <span class="routing-detail__badge">{{ routedBy }}</span>
      </div>

      <!-- Match type -->
      <div v-if="matchType" class="routing-detail__row">
        <span class="routing-detail__key">Match Type</span>
        <code class="routing-detail__val">{{ matchType }}</code>
      </div>

      <!-- Confidence bar -->
      <div v-if="confidence !== null" class="routing-detail__row">
        <span class="routing-detail__key">Confidence</span>
        <ConfidenceBar
          :confidence="confidence"
          class="routing-detail__bar"
        />
      </div>

      <!-- Direct routing indicator -->
      <div v-if="direct !== undefined" class="routing-detail__row">
        <span class="routing-detail__key">Direct</span>
        <span class="routing-detail__val" :class="{ 'routing-detail__val--yes': direct }">
          {{ direct ? "yes" : "no" }}
        </span>
      </div>
    </template>
    <template v-else>
      <span class="routing-detail__none">routing provenance not recorded</span>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from "vue";
import type { TraceEvent } from "../../types.js";
import ConfidenceBar from "./ConfidenceBar.vue";

const props = defineProps<{ event: TraceEvent }>();

const a = computed(() => props.event.attrs as Record<string, unknown>);

const routedBy   = computed(() => typeof a.value.routed_by   === "string" ? a.value.routed_by   : null);
const matchType  = computed(() => typeof a.value.match_type  === "string" ? a.value.match_type  : null);
const confidence = computed(() => typeof a.value.confidence  === "number" ? a.value.confidence  : null);
const direct     = computed(() => typeof a.value.direct      === "boolean" ? a.value.direct     : undefined);

const hasData = computed(
  () => routedBy.value !== null || matchType.value !== null || confidence.value !== null || direct.value !== undefined
);
</script>

<style scoped>
.routing-detail {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  font-size: 0.8125rem;
}

.routing-detail__row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.routing-detail__key {
  color: var(--k-fg-muted, #64748b);
  font-size: 0.75rem;
  min-width: 5.5rem;
  flex-shrink: 0;
}

.routing-detail__badge {
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  font-weight: 600;
  color: #67e8f9;
  background: #083344;
  border: 1px solid #0891b2;
  padding: 0.1rem 0.4rem;
  border-radius: 4px;
}

.routing-detail__val {
  font-family: ui-monospace, monospace;
  font-size: 0.75rem;
  color: var(--k-fg, #e2e8f0);
}

.routing-detail__val--yes {
  color: var(--k-success, #4ade80);
  font-weight: 600;
}

.routing-detail__bar {
  flex: 1;
  min-width: 8rem;
}

.routing-detail__none {
  color: var(--k-fg-subtle, #475569);
  font-size: 0.75rem;
  font-style: italic;
}
</style>
