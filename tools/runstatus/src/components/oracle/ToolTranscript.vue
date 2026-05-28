<template>
  <div class="tool-transcript">
    <div
      v-for="tc in toolCalls"
      :key="tc.seq"
      class="tool-transcript__entry"
    >
      <div class="tool-transcript__row" @click="toggle(tc.seq)">
        <span class="tool-transcript__seq">{{ tc.seq }}</span>
        <span class="tool-transcript__chip" :class="chipClass(tc.tool)">{{ tc.tool }}</span>
        <span class="tool-transcript__args">{{ summariseArgs(tc.args) }}</span>
        <span v-if="tc.duration_ms !== undefined" class="tool-transcript__dur">{{ fmtMs(tc.duration_ms) }}</span>
        <span v-if="tc.error" class="tool-transcript__err-flag">ERR</span>
        <span class="tool-transcript__toggle">{{ expanded.has(tc.seq) ? '−' : '+' }}</span>
      </div>

      <div v-if="expanded.has(tc.seq)" class="tool-transcript__detail">
        <div class="tool-transcript__detail-section">
          <span class="tool-transcript__label">Args</span>
          <pre class="tool-transcript__pre">{{ prettyJson(tc.args) }}</pre>
        </div>
        <div v-if="tc.result !== undefined" class="tool-transcript__detail-section">
          <span class="tool-transcript__label">Result</span>
          <pre class="tool-transcript__pre">{{ truncateResult(tc.result) }}</pre>
        </div>
        <div v-if="tc.error" class="tool-transcript__detail-section">
          <span class="tool-transcript__label tool-transcript__label--err">Error</span>
          <pre class="tool-transcript__pre tool-transcript__pre--err">{{ tc.error }}</pre>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { reactive, watchEffect } from "vue";
import { toolChipClass, fmtMs, prettyJson } from "./lib.js";

interface ToolCall {
  seq: number;
  tool: string;
  args: unknown;
  result?: unknown;
  duration_ms?: number;
  error?: string;
}

const props = defineProps<{ toolCalls: ToolCall[] }>();

const expanded = reactive(new Set<number>());
watchEffect(() => { for (const tc of props.toolCalls) expanded.add(tc.seq); });

function toggle(seq: number): void {
  if (expanded.has(seq)) expanded.delete(seq);
  else expanded.add(seq);
}

function chipClass(tool: string): string {
  return toolChipClass(tool);
}

function summariseArgs(args: unknown): string {
  if (args === null || args === undefined) return "";
  if (typeof args === "string") return args.slice(0, 80);
  const s = JSON.stringify(args);
  return s.length > 80 ? s.slice(0, 80) + "…" : s;
}

function truncateResult(result: unknown): string {
  const s = typeof result === "string" ? result : JSON.stringify(result, null, 2);
  return s.length > 800 ? s.slice(0, 800) + "\n…[truncated]" : s;
}
</script>

<style scoped>
.tool-transcript {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}

.tool-transcript__entry {
  border: 1px solid #1e293b;
  border-radius: 4px;
  overflow: hidden;
}

.tool-transcript__row {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.25rem 0.5rem;
  background: #0a1728;
  cursor: pointer;
  font-size: 0.75rem;
  flex-wrap: nowrap;
}

.tool-transcript__row:hover {
  background: #0f1e38;
}

.tool-transcript__seq {
  color: #475569;
  min-width: 1.5rem;
  text-align: right;
  font-family: ui-monospace, monospace;
}

.tool-transcript__chip {
  padding: 0.05rem 0.3rem;
  border-radius: 3px;
  font-size: 0.7rem;
  font-weight: 600;
  font-family: ui-monospace, monospace;
  white-space: nowrap;
}

/* tool chip colours */
.tool-chip--read    { background: #1e293b; color: #94a3b8; }
.tool-chip--edit    { background: #3a2d08; color: #fde68a; }
.tool-chip--bash    { background: #2e1065; color: #d8b4fe; }
.tool-chip--default { background: #1e293b; color: #7dd3fc; }

.tool-transcript__args {
  flex: 1;
  color: #7dd3fc;
  font-family: ui-monospace, monospace;
  font-size: 0.7rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-transcript__dur {
  color: #475569;
  font-size: 0.7rem;
  white-space: nowrap;
}

.tool-transcript__err-flag {
  background: #7f1d1d;
  color: #fca5a5;
  font-size: 0.65rem;
  padding: 0.05rem 0.25rem;
  border-radius: 2px;
}

.tool-transcript__toggle {
  color: #475569;
  font-size: 0.75rem;
  min-width: 1rem;
  text-align: center;
}

.tool-transcript__detail {
  padding: 0.35rem 0.5rem;
  background: #080f1a;
  border-top: 1px solid #1e293b;
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}

.tool-transcript__detail-section {
  display: flex;
  flex-direction: column;
  gap: 0.15rem;
}

.tool-transcript__label {
  color: #64748b;
  font-size: 0.7rem;
}

.tool-transcript__label--err {
  color: #f87171;
}

.tool-transcript__pre {
  background: #080f1a;
  border: 1px solid #1e293b;
  border-radius: 3px;
  padding: 0.3rem 0.5rem;
  font-family: ui-monospace, monospace;
  font-size: 0.7rem;
  color: #7dd3fc;
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
}

.tool-transcript__pre--err {
  color: #fca5a5;
}
</style>
