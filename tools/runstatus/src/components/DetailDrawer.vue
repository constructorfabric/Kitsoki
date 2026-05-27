<template>
  <!-- Backdrop -->
  <Teleport to="body">
    <div
      v-if="isOpen"
      class="detail-drawer__backdrop"
      @click="onClose"
    />
    <div
      v-if="isOpen"
      class="detail-drawer"
      role="dialog"
      aria-modal="true"
      tabindex="-1"
      ref="drawerRef"
      @keydown.esc="onClose"
    >
      <div class="detail-drawer__header">
        <span class="detail-drawer__title">{{ drawerTitle }}</span>
        <button class="detail-drawer__close" @click="onClose" aria-label="Close">✕</button>
      </div>

      <div class="detail-drawer__body">
        <!-- Node panel -->
        <section v-if="selectedNode" class="detail-drawer__section">
          <h3 class="detail-drawer__section-title">Node</h3>

          <!-- State node -->
          <template v-if="selectedNode.kind === 'state'">
            <div class="detail-drawer__kv">
              <span class="detail-drawer__key">Path</span>
              <code class="detail-drawer__val">{{ selectedNode.ref }}</code>
            </div>
            <div v-if="resolvedState?.Description" class="detail-drawer__kv">
              <span class="detail-drawer__key">Description</span>
              <span class="detail-drawer__val">{{ resolvedState.Description }}</span>
            </div>
            <!-- View -->
            <div v-if="resolvedState?.View" class="detail-drawer__block">
              <span class="detail-drawer__key">View</span>
              <pre class="detail-drawer__pre">{{ prettyJson(resolvedState.View) }}</pre>
            </div>
            <!-- OnEnter effects -->
            <div v-if="resolvedOnEnter.length" class="detail-drawer__block">
              <span class="detail-drawer__key">OnEnter</span>
              <div
                v-for="(eff, i) in resolvedOnEnter"
                :key="i"
                class="detail-drawer__effect"
              >
                <span class="detail-drawer__effect-invoke">{{ (eff as Record<string, unknown>).Invoke ?? (eff as Record<string, unknown>).invoke ?? '(set)' }}</span>
                <pre v-if="effectWith(eff)" class="detail-drawer__pre detail-drawer__pre--sm">{{ effectWith(eff) }}</pre>
              </div>
            </div>
            <!-- Transitions (On) -->
            <div v-if="resolvedTransitions.length" class="detail-drawer__block">
              <span class="detail-drawer__key">Transitions</span>
              <table class="detail-drawer__table">
                <thead>
                  <tr>
                    <th>Intent</th>
                    <th>Target</th>
                    <th>Guard</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="tx in resolvedTransitions" :key="tx.intent + tx.target">
                    <td><code>{{ tx.intent }}</code></td>
                    <td><code>{{ tx.target }}</code></td>
                    <td>{{ tx.guard ?? '—' }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
            <!-- Menu -->
            <div v-if="resolvedState?.Menu" class="detail-drawer__block">
              <span class="detail-drawer__key">Menu</span>
              <pre class="detail-drawer__pre">{{ prettyJson(resolvedState.Menu) }}</pre>
            </div>
            <!-- Timeout -->
            <div v-if="resolvedState?.Timeout" class="detail-drawer__kv">
              <span class="detail-drawer__key">Timeout</span>
              <code class="detail-drawer__val">{{ prettyJson(resolvedState.Timeout) }}</code>
            </div>
          </template>

          <!-- Effect node -->
          <template v-else-if="selectedNode.kind === 'effect'">
            <div class="detail-drawer__kv">
              <span class="detail-drawer__key">Ref</span>
              <code class="detail-drawer__val">{{ selectedNode.ref }}</code>
            </div>
            <template v-if="resolvedEffect">
              <div class="detail-drawer__kv">
                <span class="detail-drawer__key">Invoke</span>
                <code class="detail-drawer__val">{{ (resolvedEffect as Record<string, unknown>).Invoke ?? (resolvedEffect as Record<string, unknown>).invoke }}</code>
              </div>
              <div v-if="effectWith(resolvedEffect)" class="detail-drawer__block">
                <span class="detail-drawer__key">With</span>
                <pre class="detail-drawer__pre">{{ effectWith(resolvedEffect) }}</pre>
              </div>
              <div v-if="(resolvedEffect as Record<string, unknown>).Set || (resolvedEffect as Record<string, unknown>).Bind" class="detail-drawer__block">
                <span class="detail-drawer__key">Set / Bind</span>
                <pre class="detail-drawer__pre">{{ prettyJson({ Set: (resolvedEffect as Record<string, unknown>).Set, Bind: (resolvedEffect as Record<string, unknown>).Bind }) }}</pre>
              </div>
              <div v-if="(resolvedEffect as Record<string, unknown>).OnError" class="detail-drawer__kv">
                <span class="detail-drawer__key">OnError</span>
                <code class="detail-drawer__val">{{ (resolvedEffect as Record<string, unknown>).OnError }}</code>
              </div>
            </template>
            <div v-else class="detail-drawer__empty">Effect not found in AppDef.</div>
          </template>

          <!-- World node -->
          <template v-else-if="selectedNode.kind === 'world'">
            <div class="detail-drawer__kv">
              <span class="detail-drawer__key">Ref</span>
              <code class="detail-drawer__val">{{ selectedNode.ref }}</code>
            </div>
            <div
              v-for="varName in worldVarNames"
              :key="varName"
              class="detail-drawer__block"
            >
              <span class="detail-drawer__key">{{ varName }}</span>
              <pre v-if="worldVarDef(varName)" class="detail-drawer__pre">{{ prettyJson(worldVarDef(varName)) }}</pre>
              <span v-else class="detail-drawer__empty">Not found in World definition.</span>
            </div>
          </template>

          <!-- Transition kind (shouldn't reach here per spec but handle gracefully) -->
          <template v-else>
            <div class="detail-drawer__kv">
              <span class="detail-drawer__key">Kind</span>
              <code class="detail-drawer__val">{{ selectedNode.kind }}</code>
            </div>
            <div class="detail-drawer__kv">
              <span class="detail-drawer__key">Ref</span>
              <code class="detail-drawer__val">{{ selectedNode.ref }}</code>
            </div>
          </template>
        </section>

        <!-- Event panel -->
        <section v-if="selectedEvent" class="detail-drawer__section">
          <h3 class="detail-drawer__section-title">Event</h3>
          <div class="detail-drawer__kv">
            <span class="detail-drawer__key">Msg</span>
            <code class="detail-drawer__val">{{ selectedEvent.msg }}</code>
          </div>
          <div class="detail-drawer__kv">
            <span class="detail-drawer__key">Level</span>
            <span class="detail-drawer__val" :data-level="selectedEvent.level">{{ selectedEvent.level }}</span>
          </div>
          <div class="detail-drawer__kv">
            <span class="detail-drawer__key">Turn</span>
            <span class="detail-drawer__val">{{ selectedEvent.turn }}</span>
          </div>
          <div class="detail-drawer__kv">
            <span class="detail-drawer__key">State</span>
            <code class="detail-drawer__val">{{ selectedEvent.state_path }}</code>
          </div>
          <div class="detail-drawer__kv">
            <span class="detail-drawer__key">Time</span>
            <span class="detail-drawer__val">{{ selectedEvent.time }}</span>
          </div>

          <!-- Event-kind-specific attrs -->
          <template v-if="isOracleEvent">
            <!-- Delegate to OracleDetail for verb-specific rich rendering. -->
            <OracleDetail :event="selectedEvent" />
          </template>
          <template v-else-if="isLlmEvent">
            <div v-if="selectedEvent.attrs.prompt" class="detail-drawer__block">
              <span class="detail-drawer__key">Prompt</span>
              <pre class="detail-drawer__pre">{{ maybeShow('prompt', String(selectedEvent.attrs.prompt)) }}</pre>
              <button v-if="isTruncated(String(selectedEvent.attrs.prompt))" @click="toggleFull('prompt')" class="detail-drawer__toggle-btn">
                {{ showFull.has('prompt') ? 'Show less' : 'Show full' }}
              </button>
            </div>
            <div v-if="selectedEvent.attrs.system" class="detail-drawer__block">
              <span class="detail-drawer__key">System</span>
              <pre class="detail-drawer__pre">{{ maybeShow('system', String(selectedEvent.attrs.system)) }}</pre>
              <button v-if="isTruncated(String(selectedEvent.attrs.system))" @click="toggleFull('system')" class="detail-drawer__toggle-btn">
                {{ showFull.has('system') ? 'Show less' : 'Show full' }}
              </button>
            </div>
            <div v-if="selectedEvent.attrs.response" class="detail-drawer__block">
              <span class="detail-drawer__key">Response</span>
              <pre class="detail-drawer__pre">{{ maybeShow('response', String(selectedEvent.attrs.response)) }}</pre>
              <button v-if="isTruncated(String(selectedEvent.attrs.response))" @click="toggleFull('response')" class="detail-drawer__toggle-btn">
                {{ showFull.has('response') ? 'Show less' : 'Show full' }}
              </button>
            </div>
            <div v-if="selectedEvent.attrs.tool_calls" class="detail-drawer__block">
              <span class="detail-drawer__key">Tool Calls</span>
              <pre class="detail-drawer__pre">{{ prettyJson(selectedEvent.attrs.tool_calls) }}</pre>
            </div>
            <div v-if="selectedEvent.attrs.token_count !== undefined" class="detail-drawer__kv">
              <span class="detail-drawer__key">Token Count</span>
              <span class="detail-drawer__val">{{ selectedEvent.attrs.token_count }}</span>
            </div>
          </template>

          <template v-else-if="isHostEvent">
            <div v-if="selectedEvent.attrs.handler" class="detail-drawer__kv">
              <span class="detail-drawer__key">Handler</span>
              <code class="detail-drawer__val">{{ selectedEvent.attrs.handler }}</code>
            </div>
            <div v-if="selectedEvent.attrs.with !== undefined || selectedEvent.attrs.input !== undefined" class="detail-drawer__block">
              <span class="detail-drawer__key">Input</span>
              <pre class="detail-drawer__pre">{{ prettyJson(selectedEvent.attrs.with ?? selectedEvent.attrs.input) }}</pre>
            </div>
            <div v-if="selectedEvent.attrs.return !== undefined" class="detail-drawer__block">
              <span class="detail-drawer__key">Return</span>
              <pre class="detail-drawer__pre">{{ prettyJson(selectedEvent.attrs.return) }}</pre>
            </div>
            <div v-if="selectedEvent.attrs.duration_ms !== undefined" class="detail-drawer__kv">
              <span class="detail-drawer__key">Duration</span>
              <span class="detail-drawer__val">{{ selectedEvent.attrs.duration_ms }}ms</span>
            </div>
          </template>

          <template v-else-if="isTransitionEvent">
            <div v-if="selectedEvent.attrs.intent" class="detail-drawer__kv">
              <span class="detail-drawer__key">Intent</span>
              <code class="detail-drawer__val">{{ selectedEvent.attrs.intent }}</code>
            </div>
            <div v-if="selectedEvent.attrs.from" class="detail-drawer__kv">
              <span class="detail-drawer__key">From</span>
              <code class="detail-drawer__val">{{ selectedEvent.attrs.from }}</code>
            </div>
            <div v-if="selectedEvent.attrs.to" class="detail-drawer__kv">
              <span class="detail-drawer__key">To</span>
              <code class="detail-drawer__val">{{ selectedEvent.attrs.to }}</code>
            </div>
            <div v-if="selectedEvent.attrs.guard" class="detail-drawer__kv">
              <span class="detail-drawer__key">Guard</span>
              <code class="detail-drawer__val">{{ selectedEvent.attrs.guard }}</code>
            </div>
          </template>

          <template v-else-if="isWorldWriteEvent">
            <div v-if="selectedEvent.attrs.key" class="detail-drawer__kv">
              <span class="detail-drawer__key">Key</span>
              <code class="detail-drawer__val">{{ selectedEvent.attrs.key }}</code>
            </div>
            <div v-if="selectedEvent.attrs.value !== undefined" class="detail-drawer__block">
              <span class="detail-drawer__key">Value</span>
              <pre class="detail-drawer__pre">{{ prettyJson(selectedEvent.attrs.value) }}</pre>
            </div>
          </template>

          <!-- Default: all attrs -->
          <template v-else>
            <div class="detail-drawer__block">
              <span class="detail-drawer__key">Attrs</span>
              <pre class="detail-drawer__pre">{{ prettyJson(selectedEvent.attrs) }}</pre>
            </div>
          </template>
        </section>

        <!-- Empty state when both null -->
        <div v-if="!selectedNode && !selectedEvent" class="detail-drawer__empty detail-drawer__empty--center">
          Click a diagram node or trace event to inspect it.
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
import { ref, computed, watch, nextTick, reactive } from "vue";
import type { NodeRef, TraceEvent, AppDef } from "../types.js";
import OracleDetail from "./oracle/OracleDetail.vue";

// ---- props & emits ----------------------------------------------------------

const props = defineProps<{
  selectedNode: NodeRef | null;
  selectedEvent: TraceEvent | null;
  appDef: AppDef;
}>();

const emit = defineEmits<{
  (e: "close"): void;
}>();

// ---- open/close state -------------------------------------------------------

const drawerRef = ref<HTMLElement | null>(null);

const isOpen = computed(() => props.selectedNode !== null || props.selectedEvent !== null);

function onClose(): void {
  emit("close");
}

// Focus the drawer when it opens so Esc works immediately.
watch(isOpen, async (v) => {
  if (v) {
    await nextTick();
    drawerRef.value?.focus();
  }
});

// ---- title ------------------------------------------------------------------

const drawerTitle = computed(() => {
  if (props.selectedNode) {
    const { kind, ref } = props.selectedNode;
    return `${kind}: ${ref}`;
  }
  if (props.selectedEvent) {
    return `Event: ${props.selectedEvent.msg}`;
  }
  return "Detail";
});

// ---- helpers ----------------------------------------------------------------

function prettyJson(val: unknown): string {
  return JSON.stringify(val, null, 2);
}

const TRUNCATE_LIMIT = 500;
const showFull = reactive(new Set<string>());

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

// ---- state resolution -------------------------------------------------------

/**
 * Walk a dot-separated path like "root.active" into the nested States map
 * (which may use PascalCase "States" or lowercase "states").
 */
function walkStatePath(path: string): Record<string, unknown> | null {
  const parts = path.split(".");
  // AppDef.states is declared as Record<string, unknown>; actual data from Go
  // uses PascalCase keys (States, OnEnter, etc.).  Accept either spelling.
  const app = props.appDef as unknown as Record<string, unknown>;
  const topStates = (app["States"] ?? app["states"]) as
    | Record<string, unknown>
    | undefined;
  if (!topStates) return null;
  let current: Record<string, unknown> = topStates;

  for (let i = 0; i < parts.length; i++) {
    const seg = parts[i]!;
    // Try the segment directly (for the first level which IS a key in states)
    if (i === 0) {
      // First segment might be the top-level key in states.
      const direct = current[seg] as Record<string, unknown> | undefined;
      if (direct !== undefined) {
        current = direct;
        continue;
      }
      // It might also be a top-level state accessed by dot-separated path where
      // the first segment is the root.
      return null;
    }
    // For deeper levels, look inside States (PascalCase) or states (camelCase).
    const nested = (current["States"] ?? current["states"]) as
      | Record<string, unknown>
      | undefined;
    if (nested === undefined) return null;
    const child = nested[seg] as Record<string, unknown> | undefined;
    if (child === undefined) return null;
    current = child;
  }

  return current;
}

const resolvedState = computed<Record<string, unknown> | null>(() => {
  if (!props.selectedNode || props.selectedNode.kind !== "state") return null;
  return walkStatePath(props.selectedNode.ref);
});

const resolvedOnEnter = computed<unknown[]>(() => {
  if (!resolvedState.value) return [];
  const oe = (resolvedState.value["OnEnter"] ?? resolvedState.value["onEnter"]) as unknown[] | undefined;
  return oe ?? [];
});

interface TransitionRow {
  intent: string;
  target: string;
  guard?: string;
}

const resolvedTransitions = computed<TransitionRow[]>(() => {
  if (!resolvedState.value) return [];
  const on = (resolvedState.value["On"] ?? resolvedState.value["on"]) as
    | Record<string, unknown>
    | undefined;
  if (!on) return [];
  const rows: TransitionRow[] = [];
  for (const [intent, val] of Object.entries(on)) {
    const targets = Array.isArray(val) ? val : [val];
    for (const t of targets) {
      if (typeof t === "object" && t !== null) {
        const tx = t as Record<string, unknown>;
        rows.push({
          intent,
          target: String(tx["Target"] ?? tx["target"] ?? ""),
          guard: tx["Guard"] !== undefined ? String(tx["Guard"]) : tx["guard"] !== undefined ? String(tx["guard"]) : undefined,
        });
      }
    }
  }
  return rows;
});

// ---- effect resolution ------------------------------------------------------

const resolvedEffect = computed<Record<string, unknown> | null>(() => {
  if (!props.selectedNode || props.selectedNode.kind !== "effect") return null;
  // Ref format: "<state-path>:on_enter:<index>"
  const ref = props.selectedNode.ref;
  const colonIdx = ref.lastIndexOf(":");
  if (colonIdx === -1) return null;
  const indexStr = ref.slice(colonIdx + 1);
  const rest = ref.slice(0, colonIdx);
  // rest is "<state-path>:on_enter" or "<state-path>:<index>" (older format)
  const midIdx = rest.lastIndexOf(":");
  let statePath: string;
  if (midIdx !== -1 && rest.slice(midIdx + 1) === "on_enter") {
    statePath = rest.slice(0, midIdx);
  } else if (midIdx !== -1) {
    // Legacy: "state-path:index"
    statePath = rest.slice(0, midIdx);
  } else {
    statePath = rest;
  }

  const index = parseInt(indexStr, 10);
  if (isNaN(index)) return null;

  const state = walkStatePath(statePath);
  if (!state) return null;

  const onEnter = (state["OnEnter"] ?? state["onEnter"]) as unknown[] | undefined;
  if (!onEnter || index >= onEnter.length) return null;
  return onEnter[index] as Record<string, unknown>;
});

function effectWith(eff: unknown): string | null {
  if (typeof eff !== "object" || eff === null) return null;
  const e = eff as Record<string, unknown>;
  const w = e["With"] ?? e["with"];
  if (w === undefined) return null;
  return prettyJson(w);
}

// ---- world resolution -------------------------------------------------------

const worldVarNames = computed<string[]>(() => {
  if (!props.selectedNode || props.selectedNode.kind !== "world") return [];
  const ref = props.selectedNode.ref;
  // Format: "world:varname" or "world:a,b,c"
  const colonIdx = ref.indexOf(":");
  if (colonIdx === -1) return [];
  const names = ref.slice(colonIdx + 1);
  return names.split(",").filter(Boolean);
});

function worldVarDef(varName: string): unknown {
  const world = (props.appDef["World"] ?? props.appDef["world"]) as
    | Record<string, unknown>
    | undefined;
  if (!world) return null;
  return world[varName] ?? null;
}

// ---- event classification ---------------------------------------------------

const isOracleEvent = computed(() => {
  if (!props.selectedEvent) return false;
  return props.selectedEvent.msg.startsWith("oracle.");
});

const isLlmEvent = computed(() => {
  if (!props.selectedEvent) return false;
  const msg = props.selectedEvent.msg;
  return msg.startsWith("turn.llm.");
});

const isHostEvent = computed(() => {
  if (!props.selectedEvent) return false;
  const msg = props.selectedEvent.msg;
  return msg.startsWith("harness.") || msg.startsWith("host.");
});

const isTransitionEvent = computed(() => {
  if (!props.selectedEvent) return false;
  const msg = props.selectedEvent.msg;
  return msg.startsWith("machine.transition") || msg === "TransitionApplied";
});

const isWorldWriteEvent = computed(() => {
  if (!props.selectedEvent) return false;
  const msg = props.selectedEvent.msg;
  return msg.startsWith("machine.world.");
});
</script>

<style scoped>
/* ---- Backdrop ---- */
.detail-drawer__backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.4);
  z-index: 40;
}

/* ---- Drawer panel ---- */
.detail-drawer {
  position: fixed;
  top: 0;
  right: 0;
  height: 100%;
  width: min(480px, 100vw);
  background: #0f172a;
  border-left: 1px solid #1e293b;
  z-index: 50;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  outline: none;
  box-shadow: -4px 0 24px rgba(0, 0, 0, 0.5);
  animation: drawer-slide-in 0.15s ease;
}

@keyframes drawer-slide-in {
  from { transform: translateX(100%); opacity: 0; }
  to   { transform: translateX(0);   opacity: 1; }
}

/* ---- Header ---- */
.detail-drawer__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.75rem 1rem;
  border-bottom: 1px solid #1e293b;
  background: #0f172a;
  flex-shrink: 0;
}

.detail-drawer__title {
  font-size: 0.875rem;
  font-weight: 600;
  color: #cbd5e1;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: calc(100% - 2.5rem);
}

.detail-drawer__close {
  background: none;
  border: none;
  color: #64748b;
  cursor: pointer;
  font-size: 1rem;
  padding: 0.2rem 0.4rem;
  border-radius: 4px;
  line-height: 1;
}

.detail-drawer__close:hover {
  background: #1e293b;
  color: #e2e8f0;
}

/* ---- Body ---- */
.detail-drawer__body {
  flex: 1;
  overflow-y: auto;
  padding: 0.75rem 1rem;
  font-size: 0.8125rem;
}

/* ---- Section ---- */
.detail-drawer__section {
  margin-bottom: 1.5rem;
}

.detail-drawer__section + .detail-drawer__section {
  border-top: 1px solid #1e293b;
  padding-top: 1rem;
}

.detail-drawer__section-title {
  font-size: 0.75rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: #64748b;
  margin-bottom: 0.6rem;
}

/* ---- KV row ---- */
.detail-drawer__kv {
  display: flex;
  gap: 0.5rem;
  align-items: flex-start;
  margin-bottom: 0.35rem;
}

.detail-drawer__key {
  color: #64748b;
  min-width: 5.5rem;
  flex-shrink: 0;
  font-size: 0.75rem;
}

.detail-drawer__val {
  color: #e2e8f0;
  word-break: break-word;
}

.detail-drawer__val[data-level="warn"]  { color: #fbbf24; }
.detail-drawer__val[data-level="error"] { color: #f87171; }
.detail-drawer__val[data-level="debug"] { color: #64748b; }

/* ---- Block (key + pre) ---- */
.detail-drawer__block {
  margin-bottom: 0.6rem;
}

.detail-drawer__block > .detail-drawer__key {
  display: block;
  margin-bottom: 0.2rem;
}

/* ---- Pre / code ---- */
.detail-drawer__pre {
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

.detail-drawer__pre--sm {
  font-size: 0.7rem;
  max-height: 6rem;
  overflow-y: auto;
}

code.detail-drawer__val {
  font-family: ui-monospace, monospace;
  font-size: 0.75rem;
  color: #7dd3fc;
  background: #080f1a;
  padding: 0.05rem 0.25rem;
  border-radius: 3px;
}

/* ---- Effects list ---- */
.detail-drawer__effect {
  margin-bottom: 0.4rem;
  padding: 0.3rem 0.5rem;
  background: #0c1829;
  border: 1px solid #1e293b;
  border-radius: 4px;
}

.detail-drawer__effect-invoke {
  font-family: ui-monospace, monospace;
  font-size: 0.775rem;
  color: #c4b5fd;
}

/* ---- Table ---- */
.detail-drawer__table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.775rem;
}

.detail-drawer__table th {
  text-align: left;
  color: #64748b;
  border-bottom: 1px solid #1e293b;
  padding: 0.2rem 0.4rem;
  font-weight: 600;
}

.detail-drawer__table td {
  color: #e2e8f0;
  padding: 0.2rem 0.4rem;
  border-bottom: 1px solid #0f172a;
  vertical-align: top;
}

/* ---- Truncation toggle ---- */
.detail-drawer__toggle-btn {
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

.detail-drawer__toggle-btn:hover {
  background: #1e293b;
}

/* ---- Empty state ---- */
.detail-drawer__empty {
  color: #475569;
  font-size: 0.8125rem;
}

.detail-drawer__empty--center {
  text-align: center;
  padding: 2rem 1rem;
}
</style>
