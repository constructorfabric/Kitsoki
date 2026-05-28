<template>
  <div class="session-list">
    <div v-if="loading" class="session-list__status">Loading sessions…</div>
    <div v-else-if="error" class="session-list__status session-list__status--error">
      Failed to load sessions: {{ error }}
    </div>
    <template v-else>
      <h1 class="session-list__title">Sessions</h1>
      <div v-if="sessions.length === 0" class="session-list__status">No sessions found.</div>
      <table v-else class="session-list__table">
        <thead>
          <tr>
            <th>Session ID</th>
            <th>App</th>
            <th>State</th>
            <th>Turn</th>
            <th>Started</th>
            <th>Status</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="s in sessions"
            :key="s.session_id"
            class="session-list__row"
            @click="navigateTo(s.session_id)"
          >
            <td><code>{{ s.session_id }}</code></td>
            <td>{{ s.app_id }}</td>
            <td><code>{{ s.current_state }}</code></td>
            <td>{{ s.turn }}</td>
            <td>{{ formatDate(s.started_at) }}</td>
            <td>
              <span :class="s.terminal ? 'session-list__badge--done' : 'session-list__badge--live'">
                {{ s.terminal ? 'done' : 'live' }}
              </span>
            </td>
          </tr>
        </tbody>
      </table>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from "vue";
import { useRouter } from "vue-router";
import { createDataSource } from "../data/source.js";
import type { SessionHeader } from "../types.js";

const router = useRouter();
const sessions = ref<SessionHeader[]>([]);
const loading = ref(true);
const error = ref<string | null>(null);

onMounted(async () => {
  try {
    const src = createDataSource();
    const list = await src.listSessions();
    sessions.value = list;

    // Auto-navigate when exactly one session.
    if (list.length === 1 && list[0]) {
      router.push("/s/" + list[0].session_id);
      return;
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
});

function navigateTo(sessionId: string): void {
  router.push("/s/" + sessionId);
}

function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}
</script>

<style scoped>
.session-list {
  padding: 1.5rem;
  max-width: 900px;
  margin: 0 auto;
}

.session-list__title {
  font-size: 1.25rem;
  font-weight: 600;
  color: #e2e8f0;
  margin-bottom: 1rem;
}

.session-list__status {
  color: #94a3b8;
  font-size: 0.875rem;
  padding: 1rem 0;
}

.session-list__status--error {
  color: #f87171;
}

.session-list__table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.875rem;
}

.session-list__table th {
  text-align: left;
  color: #64748b;
  border-bottom: 1px solid #1e293b;
  padding: 0.4rem 0.6rem;
  font-weight: 600;
  font-size: 0.75rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.session-list__table td {
  color: #e2e8f0;
  padding: 0.5rem 0.6rem;
  border-bottom: 1px solid #1a2337;
}

.session-list__row {
  cursor: pointer;
}

.session-list__row:hover td {
  background: #162032;
}

.session-list__badge--live,
.session-list__badge--done {
  display: inline-block;
  padding: 0.1rem 0.4rem;
  border-radius: 999px;
  font-size: 0.7rem;
  font-weight: 600;
}

.session-list__badge--live {
  background: #14532d;
  color: #86efac;
}

.session-list__badge--done {
  background: #1e293b;
  color: #64748b;
}

code {
  font-family: ui-monospace, monospace;
  font-size: 0.8rem;
  color: #7dd3fc;
}
</style>
