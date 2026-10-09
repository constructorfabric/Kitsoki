<template>
  <div class="task-detail">
    <!-- Tab bar -->
    <div class="task-detail__tabs">
      <button
        v-for="tab in TABS"
        :key="tab"
        class="task-detail__tab"
        :class="{ 'task-detail__tab--active': activeTab === tab }"
        @click="activeTab = tab"
      >{{ tab }}</button>
    </div>

    <!-- Overview tab -->
    <template v-if="activeTab === 'Overview'">
      <div v-if="instructions" class="task-detail__block">
        <span class="od-label">Instructions</span>
        <pre class="od-pre od-pre--instructions">{{ instructionsDisplay }}</pre>
        <button v-if="instrTruncated" class="od-toggle" @click="instrExpanded = !instrExpanded">
          {{ instrExpanded ? 'Show less' : 'Show full' }}
        </button>
      </div>

      <div v-if="filesChanged.length > 0" class="task-detail__block">
        <span class="od-label">Files changed ({{ filesChanged.length }} file{{ filesChanged.length !== 1 ? 's' : '' }}, +{{ totalAdds }} / -{{ totalDels }})</span>
        <table class="task-detail__files-table">
          <thead>
            <tr>
              <th>Path</th>
              <th>Status</th>
              <th>+</th>
              <th>-</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="f in filesChanged" :key="f.path">
              <td class="task-detail__file-path">{{ f.path }}</td>
              <td><span class="task-detail__status-chip" :class="statusClass(f.status)">{{ f.status }}</span></td>
              <td class="task-detail__adds">+{{ f.additions }}</td>
              <td class="task-detail__dels">-{{ f.deletions }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <CollapsibleText label="System Prompt" :text="systemPrompt" />
      <CollapsibleText label="Prompt" :text="prompt" />
      <CollapsibleText label="Response" :text="responseText" markdown />
    </template>

    <!-- Tool transcript tab -->
    <template v-else-if="activeTab === 'Transcript'">
      <div v-if="toolCalls.length === 0" class="task-detail__empty">No tool calls recorded.</div>
      <ToolTranscript v-else :tool-calls="toolCalls" />
    </template>

    <!-- Diffs tab -->
    <template v-else-if="activeTab === 'Diffs'">
      <div v-if="filesChanged.length === 0" class="task-detail__empty">No file changes recorded.</div>
      <div v-else class="task-detail__diffs">
        <div v-for="f in filesChanged" :key="f.path" class="task-detail__diff-block">
          <div class="task-detail__diff-header" @click="toggleDiff(f.path)">
            <span class="task-detail__diff-path">{{ f.path }}</span>
            <span class="task-detail__diff-stats">+{{ f.additions }} / -{{ f.deletions }}</span>
            <span class="task-detail__diff-caret">{{ openDiffs.has(f.path) ? '−' : '+' }}</span>
          </div>
          <UnifiedDiff v-if="openDiffs.has(f.path) && f.diff" :diff="f.diff" />
        </div>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watchEffect } from "vue";
import type { TraceEvent } from "../../types.js";
import { isTruncated, maybeShow } from "./lib.js";
import CollapsibleText from "./CollapsibleText.vue";
import ToolTranscript from "./ToolTranscript.vue";
import UnifiedDiff from "./UnifiedDiff.vue";
import { usePromptLoader } from "./usePromptLoader.js";

const props = defineProps<{ event: TraceEvent }>();

const TABS = ["Overview", "Transcript", "Diffs"] as const;
type Tab = (typeof TABS)[number];

const activeTab = ref<Tab>("Overview");
const openDiffs = reactive(new Set<string>());
const instrExpanded = ref(true);

interface FileChanged {
  path: string;
  status: string;
  additions: number;
  deletions: number;
  diff?: string;
}

interface ToolCall {
  seq: number;
  tool: string;
  args: unknown;
  result?: unknown;
  duration_ms?: number;
  error?: string;
}

const attrs = computed(() => props.event.attrs);

const instructions = computed(() => {
  const inp = attrs.value.input as Record<string, unknown> | undefined;
  return typeof inp?.instructions === "string" ? inp.instructions : (typeof attrs.value.instructions === "string" ? attrs.value.instructions : "");
});

const instrTruncated = computed(() => isTruncated(instructions.value));
const instructionsDisplay = computed(() => maybeShow(instructions.value, instrExpanded.value));

const filesChanged = computed<FileChanged[]>(() => {
  const fc = attrs.value.files_changed;
  if (!Array.isArray(fc)) return [];
  return fc as FileChanged[];
});
watchEffect(() => { for (const f of filesChanged.value) openDiffs.add(f.path); });

const toolCalls = computed<ToolCall[]>(() => {
  const tc = attrs.value.tool_calls;
  if (!Array.isArray(tc)) return [];
  return tc as ToolCall[];
});

const totalAdds = computed(() => filesChanged.value.reduce((s, f) => s + (f.additions ?? 0), 0));
const totalDels = computed(() => filesChanged.value.reduce((s, f) => s + (f.deletions ?? 0), 0));

const { prompt, systemPrompt } = usePromptLoader(attrs);

// A task response is the LLM's free-text body emitted alongside the submitted
// artifact (agent.call.complete attrs.response = { text: "..." }). The data is
// in the trace; surface it in the Overview so an expanded task row shows the
// response, not just the prompt. Falls back to a plain-string response.
const responseText = computed<string>(() => {
  const r = attrs.value.response as unknown;
  if (typeof r === "string") return r;
  if (r && typeof r === "object") {
    const t = (r as Record<string, unknown>).text;
    if (typeof t === "string") return t;
  }
  return "";
});

function statusClass(status: string): string {
  switch (status) {
    case "added": return "status--added";
    case "modified": return "status--modified";
    case "deleted": return "status--deleted";
    default: return "status--other";
  }
}

function toggleDiff(path: string): void {
  if (openDiffs.has(path)) openDiffs.delete(path);
  else openDiffs.add(path);
}
</script>

<style scoped>
.task-detail {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.task-detail__tabs {
  display: flex;
  gap: 0.25rem;
  border-bottom: 1px solid var(--k-border, #1e293b);
  padding-bottom: 0.35rem;
}

.task-detail__tab {
  background: none;
  border: 1px solid #334155;
  color: var(--k-fg-muted, #64748b);
  cursor: pointer;
  padding: 0.2rem 0.6rem;
  border-radius: 3px;
  font-size: 0.75rem;
}

.task-detail__tab:hover {
  background: var(--k-bg-hover, #1e293b);
  color: var(--k-fg, #e2e8f0);
}

.task-detail__tab--active {
  background: var(--k-bg-hover, #1e293b);
  border-color: #7c2d12;
  color: #fdba74;
}

.task-detail__block {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}

.task-detail__empty {
  color: var(--k-fg-subtle, #475569);
  font-size: 0.8125rem;
  padding: 0.5rem 0;
}

.od-label {
  color: var(--k-fg-muted, #64748b);
  font-size: 0.75rem;
}

.od-pre {
  background: var(--k-bg-inset, #080f1a);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 4px;
  padding: 0.4rem 0.6rem;
  font-family: ui-monospace, monospace;
  font-size: 0.72rem;
  color: var(--k-fg-code, #7dd3fc);
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
  max-height: 14rem;
  overflow-y: auto;
}

.od-pre--instructions {
  color: var(--k-fg, #e2e8f0);
}

.od-toggle {
  align-self: flex-start;
  background: none;
  border: 1px solid #334155;
  color: var(--k-fg-accent, #60a5fa);
  cursor: pointer;
  font-size: 0.72rem;
  padding: 0.15rem 0.5rem;
  border-radius: 3px;
}

.od-toggle:hover {
  background: var(--k-bg-hover, #1e293b);
}

/* Files table */
.task-detail__files-table {
  border-collapse: collapse;
  font-size: 0.75rem;
  width: 100%;
}

.task-detail__files-table th {
  color: var(--k-fg-muted, #64748b);
  text-align: left;
  font-weight: 500;
  padding: 0.15rem 0.4rem;
  border-bottom: 1px solid var(--k-border, #1e293b);
}

.task-detail__files-table td {
  padding: 0.15rem 0.4rem;
  border-bottom: 1px solid var(--k-bg, #0f172a);
  vertical-align: middle;
}

.task-detail__file-path {
  font-family: ui-monospace, monospace;
  color: var(--k-fg, #e2e8f0);
  word-break: break-all;
}

.task-detail__adds { color: var(--k-success, #86efac); font-family: ui-monospace, monospace; }
.task-detail__dels { color: var(--k-error, #fca5a5); font-family: ui-monospace, monospace; }

.task-detail__status-chip {
  padding: 0.05rem 0.3rem;
  border-radius: 3px;
  font-size: 0.7rem;
}

.status--added    { background: var(--k-success-bg, #052e16); color: var(--k-success, #86efac); }
.status--modified { background: #2d2400; color: var(--k-warning, #fde68a); }
.status--deleted  { background: #2d0707; color: var(--k-error, #fca5a5); }
.status--other    { background: var(--k-bg-input, #1e293b); color: var(--k-fg-muted, #94a3b8); }

/* Diffs */
.task-detail__diffs {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}

.task-detail__diff-block {
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 4px;
  overflow: hidden;
}

.task-detail__diff-header {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.25rem 0.5rem;
  background: var(--k-bg-inset, #0a1728);
  cursor: pointer;
  font-size: 0.75rem;
}

.task-detail__diff-header:hover {
  background: #0f1e38;
}

.task-detail__diff-path {
  flex: 1;
  font-family: ui-monospace, monospace;
  color: var(--k-fg, #e2e8f0);
  word-break: break-all;
}

.task-detail__diff-stats {
  color: var(--k-fg-muted, #64748b);
  font-size: 0.7rem;
  white-space: nowrap;
}

.task-detail__diff-caret {
  color: var(--k-fg-subtle, #475569);
  font-size: 0.75rem;
}
</style>
