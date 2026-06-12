<template>
  <!-- A finished turn's preserved activity, collapsed by default so the final
       reply leads but the feed that produced it stays one click away (the
       exact stream the live bubble showed). Shared by the main chat transcript
       and the meta overlay; `testid` keeps the two surfaces' anchors distinct
       (chat-activity vs meta-activity) while the presentation stays one. -->
  <details v-if="items.length" class="chat-activity" :data-testid="testid">
    <summary class="chat-activity__summary" :data-testid="`${testid}-summary`">{{ activityLabel(items) }}</summary>
    <div class="chat-activity__feed" :data-testid="`${testid}-feed`">
      <ActivityFeed :items="items" />
    </div>
  </details>
</template>

<script setup lang="ts">
import { activityLabel, type StreamItem } from "../lib/activity.js";
import ActivityFeed from "./ActivityFeed.vue";

withDefaults(defineProps<{ items: StreamItem[]; testid?: string }>(), {
  testid: "chat-activity",
});
</script>

<style scoped>
.chat-activity {
  margin-bottom: 6px;
}

.chat-activity__summary {
  cursor: pointer;
  user-select: none;
  font-size: 11px;
  font-family: ui-monospace, monospace;
  color: var(--activity-muted, #6b7280);
}

.chat-activity__summary:hover {
  color: var(--activity-summary-hover, #374151);
}

.chat-activity__feed {
  margin-top: 6px;
  padding: 6px 8px;
  border: 1px solid var(--activity-border, #e2e5ea);
  border-radius: 6px;
  background: var(--activity-bg, #fcfcfd);
}
</style>
