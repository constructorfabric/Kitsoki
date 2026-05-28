<template>
  <div class="hcd">
    <!-- Command chip -->
    <div class="hcd__cmd-chip">
      <span class="hcd__cmd-prompt">$</span>
      <code class="hcd__cmd-text">{{ cmdLine }}</code>
    </div>

    <!-- Workdir badge -->
    <div v-if="workdir" class="hcd__kv">
      <span class="hcd__label">workdir</span>
      <code class="hcd__badge hcd__badge--blue">{{ workdir }}</code>
    </div>

    <!-- ── namespace-specific result sections ── -->

    <!-- host.local.run_tests -->
    <template v-if="namespace === 'host.local.run_tests'">
      <div v-if="dataMap !== null" class="hcd__kv">
        <span class="hcd__label">result</span>
        <span class="hcd__results">
          <span
            class="hcd__badge"
            :class="dataMap['ok'] === false ? 'hcd__badge--fail' : 'hcd__badge--pass'"
          >✓ {{ dataMap['passed'] ?? 0 }} passed</span>
          <span
            v-if="(dataMap['failed'] as number) > 0"
            class="hcd__badge hcd__badge--fail"
          >✗ {{ dataMap['failed'] }} failed</span>
        </span>
      </div>
      <div v-if="dataMap !== null && dataMap['log']" class="hcd__block">
        <button class="hcd__toggle-btn" @click="toggleLog">
          {{ showLog ? 'Hide output' : 'Show output' }}
        </button>
        <pre v-if="showLog" class="hcd__pre">{{ dataMap['log'] }}</pre>
      </div>
    </template>

    <!-- host.local.build -->
    <template v-else-if="namespace === 'host.local.build'">
      <div v-if="dataMap !== null" class="hcd__kv">
        <span class="hcd__label">result</span>
        <span
          class="hcd__badge"
          :class="dataMap['ok'] === false ? 'hcd__badge--fail' : 'hcd__badge--pass'"
        >{{ dataMap['ok'] === false ? '✗ failed' : '✓ ok' }}</span>
      </div>
      <div v-if="dataMap !== null && dataMap['log']" class="hcd__block">
        <button class="hcd__toggle-btn" @click="toggleLog">
          {{ showLog ? 'Hide output' : 'Show output' }}
        </button>
        <pre v-if="showLog" class="hcd__pre">{{ dataMap['log'] }}</pre>
      </div>
    </template>

    <!-- host.git.commit -->
    <template v-else-if="namespace === 'host.git.commit'">
      <div v-if="dataMap !== null" class="hcd__kv">
        <span class="hcd__label">result</span>
        <span class="hcd__results">
          <code v-if="dataMap['sha']" class="hcd__badge hcd__badge--sha">{{ dataMap['sha'] }}</code>
          <span v-if="commitFileCount !== null" class="hcd__badge hcd__badge--muted">
            {{ commitFileCount }} {{ commitFileCount === 1 ? 'file' : 'files' }}
          </span>
        </span>
      </div>
      <div v-if="dataMap !== null && dataMap['diff']" class="hcd__block">
        <button class="hcd__toggle-btn" @click="toggleLog">
          {{ showLog ? 'Hide diff' : 'Show diff' }}
        </button>
        <pre v-if="showLog" class="hcd__pre">{{ dataMap['diff'] }}</pre>
      </div>
    </template>

    <!-- host.git.diff -->
    <template v-else-if="namespace === 'host.git.diff'">
      <div v-if="dataMap !== null" class="hcd__kv">
        <span class="hcd__label">result</span>
        <span v-if="diffFileCount !== null" class="hcd__badge hcd__badge--muted">
          {{ diffFileCount }} {{ diffFileCount === 1 ? 'file' : 'files' }}
        </span>
      </div>
      <div v-if="dataMap !== null && dataMap['diff']" class="hcd__block">
        <button class="hcd__toggle-btn" @click="toggleLog">
          {{ showLog ? 'Hide diff' : 'Show diff' }}
        </button>
        <pre v-if="showLog" class="hcd__pre">{{ dataMap['diff'] }}</pre>
      </div>
    </template>

    <!-- host.git_worktree.create -->
    <template v-else-if="namespace === 'host.git_worktree.create'">
      <div v-if="dataMap !== null" class="hcd__kv">
        <span class="hcd__label">result</span>
        <span
          class="hcd__badge"
          :class="dataMap['ok'] === false ? 'hcd__badge--fail' : 'hcd__badge--pass'"
        >{{ dataMap['ok'] === false ? '✗ failed' : '✓ ok' }}</span>
      </div>
      <div v-if="dataMap !== null && dataMap['log']" class="hcd__block">
        <button class="hcd__toggle-btn" @click="toggleLog">
          {{ showLog ? 'Hide output' : 'Show output' }}
        </button>
        <pre v-if="showLog" class="hcd__pre">{{ dataMap['log'] }}</pre>
      </div>
    </template>

    <!-- host.git_worktree.sync -->
    <template v-else-if="namespace === 'host.git_worktree.sync'">
      <div v-if="dataMap !== null" class="hcd__kv">
        <span class="hcd__label">result</span>
        <span
          class="hcd__badge"
          :class="dataMap['ok'] === false ? 'hcd__badge--fail' : 'hcd__badge--pass'"
        >{{ dataMap['ok'] === false ? '✗ failed' : '✓ ok' }}</span>
      </div>
      <div v-if="dataMap !== null && dataMap['log']" class="hcd__block">
        <button class="hcd__toggle-btn" @click="toggleLog">
          {{ showLog ? 'Hide output' : 'Show output' }}
        </button>
        <pre v-if="showLog" class="hcd__pre">{{ dataMap['log'] }}</pre>
      </div>
    </template>

    <!-- Fallback: unknown namespace -->
    <template v-else>
      <div class="hcd__block">
        <span class="hcd__label hcd__label--block">Args</span>
        <pre class="hcd__pre">{{ prettyJson(args) }}</pre>
      </div>
      <div v-if="data !== undefined" class="hcd__block">
        <span class="hcd__label hcd__label--block">Return</span>
        <pre class="hcd__pre">{{ prettyJson(data) }}</pre>
      </div>
      <div v-if="error !== undefined" class="hcd__block">
        <span class="hcd__label hcd__label--block">Error</span>
        <pre class="hcd__pre hcd__pre--error">{{ prettyJson(error) }}</pre>
      </div>
    </template>

    <!-- Error block (all namespaces) -->
    <div v-if="error !== undefined && knownNamespace" class="hcd__block hcd__block--error">
      <span class="hcd__label hcd__label--block hcd__label--error">error</span>
      <pre class="hcd__pre hcd__pre--error">{{ typeof error === 'string' ? error : prettyJson(error) }}</pre>
    </div>

    <!-- Raw toggle (all namespaces) -->
    <div v-if="knownNamespace" class="hcd__raw-row">
      <button class="hcd__toggle-btn" @click="showRaw = !showRaw">{{ showRaw ? 'Hide raw' : 'Show raw' }}</button>
    </div>
    <template v-if="showRaw && knownNamespace">
      <div class="hcd__block">
        <span class="hcd__label hcd__label--block">Args</span>
        <pre class="hcd__pre">{{ prettyJson(args) }}</pre>
      </div>
      <div v-if="data !== undefined" class="hcd__block">
        <span class="hcd__label hcd__label--block">Return</span>
        <pre class="hcd__pre">{{ prettyJson(data) }}</pre>
      </div>
    </template>

    <!-- Duration -->
    <div v-if="durationMs !== null" class="hcd__kv hcd__kv--duration">
      <span class="hcd__label">duration</span>
      <span class="hcd__duration">{{ durationMs }}ms</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from "vue";

const props = defineProps<{
  namespace: string;
  args: unknown;
  data: unknown;
  error: unknown;
  durationMs: number | null;
  incomplete: boolean;
}>();

// ── helpers ──────────────────────────────────────────────────────────────────

function prettyJson(val: unknown): string {
  return JSON.stringify(val, null, 2);
}

function asMap(val: unknown): Record<string, unknown> | null {
  if (val !== null && typeof val === "object" && !Array.isArray(val)) {
    return val as Record<string, unknown>;
  }
  return null;
}

function str(val: unknown): string {
  return typeof val === "string" ? val : "";
}

// ── derived maps ──────────────────────────────────────────────────────────────

const argsMap = computed(() => asMap(props.args));
const dataMap = computed(() => asMap(props.data));

// ── known namespaces ──────────────────────────────────────────────────────────

const KNOWN = new Set([
  "host.local.run_tests",
  "host.local.build",
  "host.git.commit",
  "host.git.diff",
  "host.git_worktree.create",
  "host.git_worktree.sync",
]);

const knownNamespace = computed(() => KNOWN.has(props.namespace));

// ── workdir ───────────────────────────────────────────────────────────────────

const workdir = computed((): string | null => {
  const m = argsMap.value;
  if (!m) return null;
  const w = m["workdir"];
  return typeof w === "string" && w ? w : null;
});

// ── command line ──────────────────────────────────────────────────────────────

const cmdLine = computed((): string => {
  const m = argsMap.value ?? {};
  switch (props.namespace) {
    case "host.local.run_tests": {
      const base = str(m["test_cmd"]) || "go test ./...";
      const target = str(m["target"]);
      return target ? `${base} ${target}` : base;
    }
    case "host.local.build": {
      const base = str(m["build_cmd"]) || "go build ./...";
      const target = str(m["target"]);
      return target ? `${base} ${target}` : base;
    }
    case "host.git.commit": {
      const msg = str(m["message"]);
      const truncated = msg.length > 60 ? msg.slice(0, 60) + "…" : msg;
      return `git commit -m "${truncated}"`;
    }
    case "host.git.diff":
      return "git diff";
    case "host.git_worktree.create": {
      const name = str(m["name"]);
      const id = str(m["id"]);
      const base = str(m["base"]);
      return `git worktree add -b ${name} .worktrees/${id} ${base}`;
    }
    case "host.git_worktree.sync": {
      const id = str(m["id"]);
      return `git pull --ff-only  (worktree: ${id})`;
    }
    default:
      return props.namespace;
  }
});

// ── file counts ───────────────────────────────────────────────────────────────

const commitFileCount = computed((): number | null => {
  const d = dataMap.value;
  if (!d) return null;
  const files = d["files"];
  if (Array.isArray(files)) return files.length;
  return null;
});

const diffFileCount = computed((): number | null => {
  const d = dataMap.value;
  if (!d) return null;
  const files = d["files"];
  if (Array.isArray(files)) return files.length;
  return null;
});

// ── collapsible log / diff ────────────────────────────────────────────────────

const showLog = ref(false);
const showRaw = ref(false);

function toggleLog(): void {
  showLog.value = !showLog.value;
}
</script>

<style scoped>
.hcd {
  font-size: 0.8125rem;
  color: #e2e8f0;
}

/* command chip */
.hcd__cmd-chip {
  display: flex;
  align-items: baseline;
  gap: 0.4rem;
  background: #0a0f1a;
  border: 1px solid #334155;
  border-radius: 4px;
  padding: 0.3rem 0.6rem;
  margin-bottom: 0.6rem;
  font-family: ui-monospace, monospace;
  font-size: 0.75rem;
}

.hcd__cmd-prompt {
  color: #475569;
  flex-shrink: 0;
}

.hcd__cmd-text {
  color: #7dd3fc;
  word-break: break-all;
  font-family: ui-monospace, monospace;
}

/* key-value rows */
.hcd__kv {
  display: flex;
  gap: 0.5rem;
  align-items: flex-start;
  margin-bottom: 0.35rem;
}

.hcd__kv--duration {
  margin-top: 0.5rem;
}

.hcd__label {
  color: #94a3b8;
  min-width: 5.5rem;
  flex-shrink: 0;
  font-size: 0.75rem;
}

.hcd__label--block {
  display: block;
  margin-bottom: 0.2rem;
  min-width: unset;
}

.hcd__label--error {
  color: #f87171;
}

/* results cluster */
.hcd__results {
  display: flex;
  flex-wrap: wrap;
  gap: 0.3rem;
}

/* badges */
.hcd__badge {
  display: inline-block;
  font-size: 0.7rem;
  padding: 0.1rem 0.35rem;
  border-radius: 3px;
  font-family: ui-monospace, monospace;
  white-space: nowrap;
}

.hcd__badge--pass {
  background: #14532d;
  color: #86efac;
  border: 1px solid #166534;
}

.hcd__badge--fail {
  background: #7f1d1d;
  color: #f87171;
  border: 1px solid #991b1b;
}

.hcd__badge--sha {
  background: #1e3a5f;
  color: #93c5fd;
  border: 1px solid transparent;
}

.hcd__badge--blue {
  background: #1e3a5f;
  color: #93c5fd;
  border: 1px solid transparent;
}

.hcd__badge--muted {
  background: #1e293b;
  color: #94a3b8;
  border: 1px solid #334155;
}

/* duration value */
.hcd__duration {
  color: #fdba74;
  background: #1a0f08;
  border: 1px solid #7c2d12;
  font-size: 0.7rem;
  padding: 0.1rem 0.35rem;
  border-radius: 3px;
  font-family: ui-monospace, monospace;
}

/* block */
.hcd__block {
  margin-bottom: 0.6rem;
}

.hcd__block--error {
  margin-top: 0.4rem;
}

/* pre */
.hcd__pre {
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
  margin-top: 0.3rem;
}

.hcd__pre--error {
  color: #f87171;
  border-color: #7f1d1d;
}

/* raw row */
.hcd__raw-row {
  margin-bottom: 0.4rem;
}

/* toggle button */
.hcd__toggle-btn {
  background: none;
  border: 1px solid #334155;
  color: #60a5fa;
  cursor: pointer;
  font-size: 0.75rem;
  padding: 0.15rem 0.5rem;
  border-radius: 3px;
}

.hcd__toggle-btn:hover {
  background: #1e293b;
}
</style>
