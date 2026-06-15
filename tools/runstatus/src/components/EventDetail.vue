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

    <!-- machine.gate_decided: decision detail — state, available intents, chosen, confidence -->
    <template v-if="obsKind === 'decision' && event.msg === 'machine.gate_decided'">
      <div v-if="event.attrs.state" class="event-detail__kv">
        <span class="event-detail__key">Gate State</span>
        <code class="event-detail__val">{{ event.attrs.state }}</code>
      </div>
      <div v-if="Array.isArray(event.attrs.available_intents) && (event.attrs.available_intents as string[]).length" class="event-detail__block">
        <span class="event-detail__key">Available Intents</span>
        <div class="event-detail__intent-chips">
          <span
            v-for="intent in (event.attrs.available_intents as string[])"
            :key="intent"
            class="event-detail__intent-chip"
            :class="{ 'event-detail__intent-chip--chosen': intent === event.attrs.chosen_intent }"
          >{{ intent }}</span>
        </div>
      </div>
      <div v-if="event.attrs.decider" class="event-detail__kv">
        <span class="event-detail__key">Decider</span>
        <code class="event-detail__val">{{ event.attrs.decider }}</code>
      </div>
      <div v-if="event.attrs.chosen_intent" class="event-detail__kv">
        <span class="event-detail__key">Chosen</span>
        <code class="event-detail__val event-detail__val--chosen">{{ event.attrs.chosen_intent }}</code>
      </div>
      <div v-if="event.attrs.confidence != null" class="event-detail__kv event-detail__kv--bar">
        <span class="event-detail__key">Confidence</span>
        <ConfidenceBar
          :confidence="(event.attrs.confidence as number)"
          class="event-detail__conf-bar"
        />
      </div>
      <div v-if="event.attrs.bailed_to_human" class="event-detail__kv">
        <span class="event-detail__key">Bailed to Human</span>
        <span class="event-detail__val event-detail__val--warn">yes</span>
      </div>
      <div v-if="event.attrs.reason" class="event-detail__block">
        <span class="event-detail__key">Reason</span>
        <pre class="event-detail__pre">{{ event.attrs.reason }}</pre>
      </div>
    </template>

    <!-- machine.write_mode_granted: the write-mode gate's recorded opt-in / denial -->
    <template v-else-if="obsKind === 'decision' && event.msg === 'machine.write_mode_granted'">
      <div v-if="event.attrs.state" class="event-detail__kv">
        <span class="event-detail__key">Room</span>
        <code class="event-detail__val">{{ event.attrs.state }}</code>
      </div>
      <div v-if="event.attrs.action" class="event-detail__kv">
        <span class="event-detail__key">Action</span>
        <code class="event-detail__val">{{ event.attrs.action }}</code>
      </div>
      <div v-if="event.attrs.effect" class="event-detail__kv">
        <span class="event-detail__key">Effect</span>
        <code class="event-detail__val">{{ event.attrs.effect }}</code>
      </div>
      <div class="event-detail__kv">
        <span class="event-detail__key">Granted</span>
        <span
          class="event-detail__val"
          :class="event.attrs.granted ? 'event-detail__val--chosen' : 'event-detail__val--warn'"
        >{{ event.attrs.granted ? 'yes' : 'no' }}</span>
      </div>
      <div v-if="event.attrs.scope" class="event-detail__kv">
        <span class="event-detail__key">Scope</span>
        <code class="event-detail__val">{{ event.attrs.scope }}</code>
      </div>
      <div v-if="event.attrs.by" class="event-detail__kv">
        <span class="event-detail__key">By</span>
        <code class="event-detail__val">{{ event.attrs.by }}</code>
      </div>
    </template>

    <!-- turn.start: routing detail — shows how the turn was routed -->
    <template v-else-if="obsKind === 'routing' && event.msg === 'turn.start'">
      <RoutingDetail :event="event" />
      <div class="event-detail__block">
        <div class="event-detail__attrs-header">
          <span class="event-detail__key">Attrs</span>
          <button class="event-detail__copy-btn" @click="copyAttrs">Copy</button>
        </div>
        <pre class="event-detail__pre">{{ prettyJson(event.attrs) }}</pre>
      </div>
    </template>

    <template v-else-if="isOracleComplete">
      <OracleDetail :event="event" :session-id="resolvedSessionId" />
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

    <template v-else>
      <div class="event-detail__block">
        <div class="event-detail__attrs-header">
          <span class="event-detail__key">Attrs</span>
          <button class="event-detail__copy-btn" @click="copyAttrs">Copy</button>
        </div>
        <pre class="event-detail__pre">{{ prettyJson(event.attrs) }}</pre>
      </div>
    </template>

    <!-- Annotate button: shown at the bottom when a live session_id is available. -->
    <AnnotateButton
      v-if="resolvedSessionId"
      :session-id="resolvedSessionId"
      :target-call-id="event.attrs.call_id as string | undefined"
      :target-turn="event.turn > 0 ? event.turn : undefined"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, reactive } from "vue";
import type { TraceEvent } from "../types.js";
import OracleDetail from "./oracle/OracleDetail.vue";
import ConfidenceBar from "./oracle/ConfidenceBar.vue";
import RoutingDetail from "./oracle/RoutingDetail.vue";
import HostCliDetail from "./HostCliDetail.vue";
import HostBuiltinDetail from "./HostBuiltinDetail.vue";
import AnnotateButton from "./AnnotateButton.vue";
import { observationKind } from "../lib/observation.js";

interface HarnessCallProp {
  namespace: string;
  args: unknown;
  data: unknown;
  error: unknown;
  durationMs: number | null;
  incomplete: boolean;
}

const props = defineProps<{
  event: TraceEvent;
  harnessCall?: HarnessCallProp;
  /** Optional explicit session id. Falls back to event.session_id. */
  sessionId?: string;
}>();
const { harnessCall } = props;

// Resolve the session id: explicit prop takes precedence, then event.session_id.
const resolvedSessionId = computed(() =>
  (props.sessionId || props.event.session_id) || ""
);

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

// Observation kind — used by the new decision/routing detail branches.
const obsKind = computed(() => observationKind(props.event.msg));

// oracle.call.complete events get a rich sub-renderer (OracleDetail), which
// reads attrs.verb to route to the per-verb body.
const CLI_NAMESPACES = new Set([
  "host.local.run_tests", "host.local.build",
  "host.git.commit", "host.git.diff",
  "host.git_worktree.create", "host.git_worktree.sync",
]);
function isCliNamespace(ns: string): boolean { return CLI_NAMESPACES.has(ns); }

// Canonical oracle completion: the engine emits oracle.call.complete with the
// verb in attrs.verb. OracleDetail reads attrs.verb to route to the per-verb
// sub-renderer (DecideDetail / TaskDetail / …).
// oracle.call.error ALSO routes to OracleDetail: a failed call still has a verb to
// render and MAY carry a transcript_ref (the partial agent actions up to the
// failure), which OracleDetail surfaces as the Agent-actions affordance. OracleDetail
// guards missing success fields with its error banner + raw-attrs fallback.
const isOracleComplete = computed(
  () => props.event.msg === "oracle.call.complete" || props.event.msg === "oracle.call.error"
);

// Legacy: oracle.* start/other + turn.llm.* get the old prompt/response dump.
const isLlmEvent = computed(() => !isOracleComplete.value && (props.event.msg.startsWith("oracle.") || props.event.msg.startsWith("turn.llm.")));
const isHostEvent = computed(() => props.event.msg.startsWith("harness.") || props.event.msg.startsWith("host."));
const isTransitionEvent = computed(() => props.event.msg.startsWith("machine.transition") || props.event.msg === "TransitionApplied");
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
  color: var(--k-fg-muted, #64748b);
  min-width: 5.5rem;
  flex-shrink: 0;
  font-size: 0.75rem;
}

.event-detail__val {
  color: var(--k-fg, #e2e8f0);
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
  background: var(--k-bg-deep, #080f1a);
  border: 1px solid var(--k-border, #1e293b);
  border-radius: 4px;
  padding: 0.4rem 0.6rem;
  font-family: ui-monospace, monospace;
  font-size: 0.75rem;
  color: var(--k-fg-code, #7dd3fc);
  white-space: pre-wrap;
  word-break: break-word;
  margin: 0;
}

code.event-detail__val {
  font-family: ui-monospace, monospace;
  font-size: 0.75rem;
  color: var(--k-fg-code, #7dd3fc);
  background: var(--k-bg-deep, #080f1a);
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
  background: var(--k-bg-input, #1e293b);
  border: 1px solid var(--k-border, #334155);
  color: var(--k-fg-muted, #94a3b8);
  cursor: pointer;
  padding: 0.1rem 0.4rem;
  border-radius: 3px;
  font-size: 0.7rem;
}

.event-detail__copy-btn:hover {
  background: var(--k-bg-hover, #334155);
  color: var(--k-fg, #e2e8f0);
}

.event-detail__pre--error {
  color: var(--k-error, #f87171);
  border-color: #7f1d1d;
}

.event-detail__toggle-btn {
  display: block;
  margin-top: 0.3rem;
  background: none;
  border: 1px solid var(--k-border, #334155);
  color: var(--k-fg-accent, #60a5fa);
  cursor: pointer;
  font-size: 0.75rem;
  padding: 0.15rem 0.5rem;
  border-radius: 3px;
}

.event-detail__toggle-btn:hover {
  background: var(--k-bg-hover, #1e293b);
}

/* Decision detail — intent chips */
.event-detail__intent-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 0.25rem;
  margin-top: 0.2rem;
}

.event-detail__intent-chip {
  font-family: ui-monospace, monospace;
  font-size: 0.7rem;
  padding: 0.1rem 0.4rem;
  border-radius: 3px;
  background: var(--k-bg-input, #1e293b);
  border: 1px solid var(--k-border, #334155);
  color: var(--k-fg-muted, #94a3b8);
}

.event-detail__intent-chip--chosen {
  background: var(--k-bg-selection, #1e3a5f);
  border-color: var(--k-border-focus, #3b82f6);
  color: var(--k-fg-accent, #93c5fd);
  font-weight: 600;
}

.event-detail__val--chosen {
  color: var(--k-fg-accent, #93c5fd);
  font-weight: 600;
}

.event-detail__val--warn {
  color: var(--k-warning, #fbbf24);
  font-weight: 600;
}

.event-detail__kv--bar {
  align-items: center;
}

.event-detail__conf-bar {
  flex: 1;
  min-width: 8rem;
}
</style>
