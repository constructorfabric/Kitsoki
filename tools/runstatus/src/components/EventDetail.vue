<template>
  <div class="event-detail">
    <div class="event-detail__kv">
      <span class="event-detail__key">State</span>
      <code class="event-detail__val">{{ event.state_path }}</code>
    </div>
    <div class="event-detail__kv">
      <span class="event-detail__key">Time</span>
      <span class="event-detail__val">{{ event.time }}</span>
    </div>

    <template v-if="isOracleComplete">
      <OracleDetail :event="event" />
    </template>

    <template v-else-if="isLlmEvent">
      <div v-if="event.attrs.prompt" class="event-detail__block">
        <span class="event-detail__key">Prompt</span>
        <pre class="event-detail__pre">{{ maybeShow('prompt', String(event.attrs.prompt)) }}</pre>
        <button v-if="isTruncated(String(event.attrs.prompt))" @click="toggleFull('prompt')" class="event-detail__toggle-btn">
          {{ showFull.has('prompt') ? 'Show less' : 'Show full' }}
        </button>
      </div>
      <div v-if="event.attrs.system" class="event-detail__block">
        <span class="event-detail__key">System</span>
        <pre class="event-detail__pre">{{ maybeShow('system', String(event.attrs.system)) }}</pre>
        <button v-if="isTruncated(String(event.attrs.system))" @click="toggleFull('system')" class="event-detail__toggle-btn">
          {{ showFull.has('system') ? 'Show less' : 'Show full' }}
        </button>
      </div>
      <div v-if="event.attrs.response" class="event-detail__block">
        <span class="event-detail__key">Response</span>
        <pre class="event-detail__pre">{{ maybeShow('response', String(event.attrs.response)) }}</pre>
        <button v-if="isTruncated(String(event.attrs.response))" @click="toggleFull('response')" class="event-detail__toggle-btn">
          {{ showFull.has('response') ? 'Show less' : 'Show full' }}
        </button>
      </div>
      <div v-if="event.attrs.tool_calls" class="event-detail__block">
        <span class="event-detail__key">Tool Calls</span>
        <pre class="event-detail__pre">{{ prettyJson(event.attrs.tool_calls) }}</pre>
      </div>
      <div v-if="event.attrs.token_count !== undefined" class="event-detail__kv">
        <span class="event-detail__key">Token Count</span>
        <span class="event-detail__val">{{ event.attrs.token_count }}</span>
      </div>
    </template>

    <!-- Merged harness call: route to specialized per-namespace renderers. -->
    <template v-else-if="harnessCall">
      <HostCliDetail
        v-if="isCliNamespace(harnessCall.namespace)"
        v-bind="harnessCall"
      />
      <HostBuiltinDetail
        v-else
        v-bind="harnessCall"
      />
    </template>

    <template v-else-if="isHostEvent">
      <div v-if="event.attrs.namespace || event.attrs.handler" class="event-detail__kv">
        <span class="event-detail__key">Namespace</span>
        <code class="event-detail__val">{{ event.attrs.namespace ?? event.attrs.handler }}</code>
      </div>
      <div v-if="event.attrs.background !== undefined" class="event-detail__kv">
        <span class="event-detail__key">Background</span>
        <span class="event-detail__val">{{ event.attrs.background }}</span>
      </div>
      <div v-if="event.attrs.args !== undefined || event.attrs.with !== undefined || event.attrs.input !== undefined" class="event-detail__block">
        <span class="event-detail__key">Args</span>
        <pre class="event-detail__pre">{{ prettyJson(event.attrs.args ?? event.attrs.with ?? event.attrs.input) }}</pre>
      </div>
      <div v-if="event.attrs.data !== undefined || event.attrs.return !== undefined" class="event-detail__block">
        <span class="event-detail__key">Return</span>
        <pre class="event-detail__pre">{{ prettyJson(event.attrs.data ?? event.attrs.return) }}</pre>
      </div>
      <div v-if="event.attrs.error !== undefined" class="event-detail__block">
        <span class="event-detail__key">Error</span>
        <pre class="event-detail__pre event-detail__pre--error">{{ prettyJson(event.attrs.error) }}</pre>
      </div>
      <div v-if="event.attrs.duration_ms !== undefined" class="event-detail__kv">
        <span class="event-detail__key">Duration</span>
        <span class="event-detail__val">{{ event.attrs.duration_ms }}ms</span>
      </div>
    </template>

    <template v-else-if="isTransitionEvent">
      <div v-if="event.attrs.intent" class="event-detail__kv">
        <span class="event-detail__key">Intent</span>
        <code class="event-detail__val">{{ event.attrs.intent }}</code>
      </div>
      <div v-if="event.attrs.from" class="event-detail__kv">
        <span class="event-detail__key">From</span>
        <code class="event-detail__val">{{ event.attrs.from }}</code>
      </div>
      <div v-if="event.attrs.to" class="event-detail__kv">
        <span class="event-detail__key">To</span>
        <code class="event-detail__val">{{ event.attrs.to }}</code>
      </div>
      <div v-if="event.attrs.guard" class="event-detail__kv">
        <span class="event-detail__key">Guard</span>
        <code class="event-detail__val">{{ event.attrs.guard }}</code>
      </div>
    </template>

    <template v-else-if="isWorldWriteEvent">
      <div v-if="event.attrs.key" class="event-detail__kv">
        <span class="event-detail__key">Key</span>
        <code class="event-detail__val">{{ event.attrs.key }}</code>
      </div>
      <div v-if="event.attrs.value !== undefined" class="event-detail__block">
        <span class="event-detail__key">Value</span>
        <pre class="event-detail__pre">{{ prettyJson(event.attrs.value) }}</pre>
      </div>
    </template>

    <template v-else>
      <div class="event-detail__block">
        <div class="event-detail__attrs-header">
          <span class="event-detail__key">Attrs</span>
          <button class="event-detail__copy-btn" @click="copyAttrs">Copy</button>
        </div>
        <pre class="event-detail__pre">{{ prettyJson(event.attrs) }}</pre>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive } from "vue";
import type { TraceEvent } from "../types.js";
import OracleDetail from "./oracle/OracleDetail.vue";
import HostCliDetail from "./HostCliDetail.vue";
import HostBuiltinDetail from "./HostBuiltinDetail.vue";

interface HarnessCallProp {
  namespace: string;
  args: unknown;
  data: unknown;
  error: unknown;
  durationMs: number | null;
  incomplete: boolean;
}

const props = defineProps<{ event: TraceEvent; harnessCall?: HarnessCallProp }>();
const { harnessCall } = props;

const TRUNCATE_LIMIT = 500;
const showFull = reactive(new Set<string>());

function prettyJson(val: unknown): string {
  return JSON.stringify(val, null, 2);
}
function toggleFull(key: string): void {
  if (showFull.has(key)) showFull.delete(key);
  else showFull.add(key);
}
function isTruncated(s: string): boolean {
  return s.length > TRUNCATE_LIMIT;
}
function maybeShow(key: string, val: string): string {
  if (!showFull.has(key) && val.length > TRUNCATE_LIMIT) {
    return val.slice(0, TRUNCATE_LIMIT) + "…";
  }
  return val;
}
async function copyAttrs(): Promise<void> {
  try {
    await navigator.clipboard.writeText(JSON.stringify(props.event.attrs, null, 2));
  } catch {
    // ignore
  }
}

// oracle.<verb>.complete events get a rich sub-renderer.  Verb is taken from
// attrs.verb when present, else inferred from the msg ("oracle.ask.complete"
// → "ask") so lean traces without the full attrs payload still route here.
const CLI_NAMESPACES = new Set([
  "host.local.run_tests", "host.local.build",
  "host.git.commit", "host.git.diff",
  "host.git_worktree.create", "host.git_worktree.sync",
]);
function isCliNamespace(ns: string): boolean { return CLI_NAMESPACES.has(ns); }

const ORACLE_COMPLETE_RE = /^oracle\.(decide|extract|ask|task|converse)\.complete$/;
const isOracleComplete = computed(() => ORACLE_COMPLETE_RE.test(props.event.msg));

// Legacy: oracle.* start/other + turn.llm.* get the old prompt/response dump.
const isLlmEvent = computed(() => !isOracleComplete.value && (props.event.msg.startsWith("oracle.") || props.event.msg.startsWith("turn.llm.")));
const isHostEvent = computed(() => props.event.msg.startsWith("harness.") || props.event.msg.startsWith("host."));
const isTransitionEvent = computed(() => props.event.msg.startsWith("machine.transition") || props.event.msg === "TransitionApplied");
const isWorldWriteEvent = computed(() => props.event.msg.startsWith("machine.world."));
</script>

<style scoped>
.event-detail {
  font-size: 0.8125rem;
}

.event-detail__kv {
  display: flex;
  gap: 0.5rem;
  align-items: flex-start;
  margin-bottom: 0.35rem;
}

.event-detail__key {
  color: #64748b;
  min-width: 5.5rem;
  flex-shrink: 0;
  font-size: 0.75rem;
}

.event-detail__val {
  color: #e2e8f0;
  word-break: break-word;
}

.event-detail__block {
  margin-bottom: 0.6rem;
}

.event-detail__block > .event-detail__key {
  display: block;
  margin-bottom: 0.2rem;
}

.event-detail__pre {
  background: #080f1a;
  border: 1px solid #1e293b;
  border-radius: 4px;
  padding: 0.4rem 0.6rem;
  font-family: ui-monospace, monospace;
  font-size: 0.75rem;
  color: #7dd3fc;
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
}

code.event-detail__val {
  font-family: ui-monospace, monospace;
  font-size: 0.75rem;
  color: #7dd3fc;
  background: #080f1a;
  padding: 0.05rem 0.25rem;
  border-radius: 3px;
}

.event-detail__attrs-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 0.3rem;
}

.event-detail__copy-btn {
  background: #1e293b;
  border: 1px solid #334155;
  color: #94a3b8;
  cursor: pointer;
  padding: 0.1rem 0.4rem;
  border-radius: 3px;
  font-size: 0.7rem;
}

.event-detail__copy-btn:hover {
  background: #334155;
  color: #e2e8f0;
}

.event-detail__pre--error {
  color: #f87171;
  border-color: #7f1d1d;
}

.event-detail__toggle-btn {
  display: block;
  margin-top: 0.3rem;
  background: none;
  border: 1px solid #334155;
  color: #60a5fa;
  cursor: pointer;
  font-size: 0.75rem;
  padding: 0.15rem 0.5rem;
  border-radius: 3px;
}

.event-detail__toggle-btn:hover {
  background: #1e293b;
}
</style>
