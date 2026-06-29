<script setup lang="ts">
// BugReportModal — review-before-file surface for a bug report.
//
// Shown while the bugReport store is in "reviewing": the operator inspects the
// captured rrweb replay, scrubbed HAR, and console/errors, edits the title and
// optional description, then submits (files the held capture) or cancels.
//
// Mirrors OperatorQuestionModal: Teleport to <body>, store-driven v-if, high
// z-index. The visual evidence is the rrweb session replay — a faithful render
// of the exact DOM the operator saw (scrub back for earlier frames). A replay
// error never blocks the modal; the HAR + console panes still carry the rest.
import { computed, ref, watch, onBeforeUnmount } from "vue";
import { useBugReportStore } from "../stores/bugReport.js";
import type { HarEntry } from "../data/live-source.js";

const store = useBugReportStore();

const open = computed(() => store.status === "reviewing");

const harEntries = computed<HarEntry[]>(
  () => store.har?.log?.entries ?? []
);
const showRawHar = ref(false);
const rawHar = computed(() => JSON.stringify(store.har ?? {}, null, 2));
const showConsole = ref(false);

const consoleEntries = computed(
  () =>
    (store.consoleLogs as Array<{ level: string; text: string }>) ?? []
);
const errorCount = computed(() => {
  const info = store.errorInfo as { errors?: unknown[] } | null;
  return info?.errors?.length ?? 0;
});

function statusOf(e: HarEntry): string {
  const s = e.response?.status;
  return s === undefined ? "—" : String(s);
}

// --- replay (rrweb Replayer, lazy) ---
//
// We drive rrweb's own Replayer directly rather than the `rrweb-player` Svelte
// wrapper: the published rrweb-player@2.0.1 ESM build ships a Player component
// whose compiled `instance` never instantiates the Replayer (no onMount), so it
// renders an empty frame — a blank white box. rrweb's Replayer builds the real
// iframe + DOM reconstruction and exposes play/pause/getMetaData, which is all
// we need. We wrap it with a minimal play / restart control and a scrub slider.
interface RrwebReplayer {
  wrapper: HTMLElement;
  iframe: HTMLIFrameElement;
  play(timeOffset?: number): void;
  pause(timeOffset?: number): void;
  getCurrentTime(): number;
  getMetaData(): { startTime: number; endTime: number; totalTime: number };
  destroy(): void;
  on(event: string, handler: (...a: unknown[]) => void): unknown;
}

const replayHost = ref<HTMLElement | null>(null);
let player: RrwebReplayer | null = null;
let scrubTimer: ReturnType<typeof setInterval> | null = null;
// True once a replay is actually rendered; drives the "Loading replay…"
// placeholder so the pane is never a silent blank box.
const replayReady = ref(false);
const isPlaying = ref(false);
const totalMs = ref(0);
const currentMs = ref(0);
const hasEvents = computed(() => (store.rrwebEvents as unknown[]).length >= 2);

function stopScrubTimer(): void {
  if (scrubTimer) {
    clearInterval(scrubTimer);
    scrubTimer = null;
  }
}

async function mountPlayer(): Promise<void> {
  replayReady.value = false;
  isPlaying.value = false;
  currentMs.value = 0;
  const events = store.rrwebEvents as unknown[];
  if (!replayHost.value || events.length < 2) return;
  try {
    const mod = await import("rrweb");
    await import("rrweb/dist/style.css");
    const Replayer = (mod as { Replayer?: unknown }).Replayer as
      | (new (e: unknown[], c: Record<string, unknown>) => RrwebReplayer)
      | undefined;
    if (!Replayer) return;
    player = new Replayer(events, {
      root: replayHost.value,
      speed: 1,
      skipInactive: true,
      showWarning: false,
      mouseTail: false,
      // The captured DOM may reference styles via <link>; let rrweb inline what
      // it snapshotted. A blank iframe here still beats the old empty box.
    });
    const meta = player.getMetaData();
    totalMs.value = Math.max(0, meta.totalTime || 0);
    // Pause on the LAST frame — the state at the moment the bug was reported,
    // the richest, most relevant reconstruction (the first full snapshot can be
    // near-empty if recording started at app boot). The operator scrubs back for
    // earlier frames. Showing a populated frame avoids a deceptively empty pane.
    currentMs.value = totalMs.value;
    player.pause(totalMs.value);
    // rrweb's Replayer sizes the iframe at the CAPTURED viewport (e.g. 1600×900)
    // and does NOT scale to fit — that's the job rrweb-player's controller would
    // have done. Scale the wrapper down to fit the host so the whole frame is
    // visible (rrweb-player applies the same transform).
    scaleReplayToFit();
    replayReady.value = true;
  } catch {
    // Replay couldn't render — surface the placeholder instead of a blank box.
    player = null;
    replayReady.value = false;
  }
}

function scaleReplayToFit(): void {
  const host = replayHost.value;
  const wrapper = player?.wrapper;
  const iframe = player?.iframe;
  if (!host || !wrapper || !iframe) return;
  const fw = iframe.offsetWidth || parseFloat(iframe.style.width) || 0;
  const fh = iframe.offsetHeight || parseFloat(iframe.style.height) || 0;
  if (!fw || !fh) return;
  const scale = Math.min(host.clientWidth / fw, host.clientHeight / fh, 1);
  wrapper.style.transform = `translate(-50%, -50%) scale(${scale})`;
  wrapper.style.transformOrigin = "center center";
}

function destroyPlayer(): void {
  stopScrubTimer();
  try {
    player?.destroy();
  } catch {
    /* ignore */
  }
  player = null;
  isPlaying.value = false;
}

function togglePlay(): void {
  if (!player) return;
  if (isPlaying.value) {
    player.pause();
    isPlaying.value = false;
    stopScrubTimer();
    return;
  }
  // Restart from the top if we're at (or past) the end.
  const from = currentMs.value >= totalMs.value ? 0 : currentMs.value;
  player.play(from);
  isPlaying.value = true;
  stopScrubTimer();
  scrubTimer = setInterval(() => {
    if (!player) return;
    currentMs.value = Math.min(player.getCurrentTime(), totalMs.value);
    if (currentMs.value >= totalMs.value) {
      isPlaying.value = false;
      stopScrubTimer();
    }
  }, 100);
}

function onScrub(e: Event): void {
  if (!player) return;
  const t = Number((e.target as HTMLInputElement).value);
  currentMs.value = t;
  player.pause(t);
  isPlaying.value = false;
  stopScrubTimer();
}

watch(open, async (isOpen) => {
  if (isOpen) {
    showRawHar.value = false;
    showConsole.value = false;
    // Wait for the host element to render, then mount the player.
    await Promise.resolve();
    await mountPlayer();
  } else {
    destroyPlayer();
  }
});

onBeforeUnmount(destroyPlayer);

async function onSubmit(): Promise<void> {
  await store.submit();
}
function onCancel(): void {
  store.cancel();
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="br-backdrop"
      role="dialog"
      aria-modal="true"
      data-testid="bug-modal"
    >
      <div class="br-panel">
        <header class="br-header">
          <span class="br-glyph">🐞</span>
          <span class="br-title">Review bug report</span>
          <span class="br-depth">{{ harEntries.length }} RPC exchange(s)</span>
        </header>

        <div class="br-body">
          <!-- Session replay (the visual evidence) -->
          <section class="br-section" data-testid="bug-modal-replay">
            <h4 class="br-h">Session replay</h4>
            <div ref="replayHost" class="br-replay-host">
              <p v-if="!hasEvents" class="br-muted">
                No session replay captured.
              </p>
              <p v-else-if="!replayReady" class="br-muted">
                Loading replay…
              </p>
            </div>
            <div v-if="replayReady" class="br-replay-ctl">
              <button
                type="button"
                class="br-toggle"
                data-testid="bug-modal-replay-play"
                @click="togglePlay"
              >
                {{ isPlaying ? "Pause" : currentMs >= totalMs ? "Replay" : "Play" }}
              </button>
              <input
                class="br-scrub"
                type="range"
                min="0"
                :max="totalMs"
                step="50"
                :value="currentMs"
                data-testid="bug-modal-replay-scrub"
                @input="onScrub"
              />
            </div>
          </section>

          <!-- HAR -->
          <section class="br-section">
            <h4 class="br-h">Network trace (scrubbed)</h4>
            <ul class="br-har" data-testid="bug-modal-har-summary">
              <li
                v-for="(e, i) in harEntries"
                :key="i"
                class="br-har-row"
                data-testid="bug-modal-har-row"
              >
                <span class="br-method">{{ e.request?.method ?? "?" }}</span>
                <span class="br-url">{{ e.request?.url ?? "" }}</span>
                <span class="br-status">{{ statusOf(e) }}</span>
              </li>
              <li v-if="harEntries.length === 0" class="br-muted">
                No exchanges captured.
              </li>
            </ul>
            <button
              type="button"
              class="br-toggle"
              data-testid="bug-modal-har-raw-toggle"
              @click="showRawHar = !showRawHar"
            >
              {{ showRawHar ? "Hide raw HAR" : "Show raw HAR" }}
            </button>
            <pre
              v-if="showRawHar"
              class="br-raw"
              data-testid="bug-modal-har-raw"
            >{{ rawHar }}</pre>
          </section>

          <!-- Console / errors -->
          <section class="br-section">
            <button
              type="button"
              class="br-toggle"
              @click="showConsole = !showConsole"
            >
              Console &amp; errors ({{ consoleEntries.length }} logs,
              {{ errorCount }} errors)
            </button>
            <ul
              v-if="showConsole"
              class="br-console"
              data-testid="bug-modal-console"
            >
              <li
                v-for="(c, i) in consoleEntries"
                :key="i"
                class="br-console-row"
              >
                <span class="br-level">{{ c.level }}</span>
                <span class="br-text">{{ c.text }}</span>
              </li>
              <li v-if="consoleEntries.length === 0" class="br-muted">
                No recent console output.
              </li>
            </ul>
          </section>

          <!-- Click placement -->
          <section
            v-if="store.placement"
            class="br-section"
            data-testid="bug-modal-placement"
          >
            <h4 class="br-h">Clicked location</h4>
            <dl class="br-placement">
              <div>
                <dt>Target</dt>
                <dd data-testid="bug-modal-placement-target">
                  {{ store.placement.selector }}
                </dd>
              </div>
              <div>
                <dt>Viewport</dt>
                <dd data-testid="bug-modal-placement-point">
                  {{ Math.round(store.placement.x) }},
                  {{ Math.round(store.placement.y) }}
                </dd>
              </div>
              <div v-if="store.placement.text">
                <dt>Text</dt>
                <dd data-testid="bug-modal-placement-text">
                  {{ store.placement.text }}
                </dd>
              </div>
            </dl>
          </section>

          <!-- Title + description -->
          <section class="br-section">
            <label class="br-label" for="br-title">Title</label>
            <input
              id="br-title"
              v-model="store.title"
              class="br-input"
              type="text"
              data-testid="bug-modal-title"
            />
            <label class="br-label" for="br-desc">
              Description (optional)
            </label>
            <textarea
              id="br-desc"
              v-model="store.description"
              class="br-textarea"
              rows="4"
              placeholder="What went wrong? What did you expect?"
              data-testid="bug-modal-description"
            ></textarea>
          </section>
        </div>

        <footer class="br-footer">
          <button
            type="button"
            class="br-cancel"
            data-testid="bug-modal-cancel"
            @click="onCancel"
          >
            Cancel
          </button>
          <button
            type="button"
            class="br-submit"
            data-testid="bug-modal-submit"
            :disabled="store.status === 'submitting'"
            @click="onSubmit"
          >
            {{ store.status === "submitting" ? "Filing…" : "File bug" }}
          </button>
        </footer>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.br-backdrop {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1200;
}
.br-panel {
  background: var(--k-bg-widget, #0d1b2a);
  border: 1px solid #1e3a5f;
  border-radius: 10px;
  box-shadow: 0 8px 40px rgba(0, 0, 0, 0.5);
  display: flex;
  flex-direction: column;
  width: min(620px, 94vw);
  max-height: 88vh;
  overflow: hidden;
  color: var(--k-fg, #e2e8f0);
}
.br-header {
  display: flex;
  align-items: center;
  gap: 0.55rem;
  padding: 0.85rem 1.1rem;
  border-bottom: 1px solid #1e3a5f;
  background: var(--k-bg-deep, #0a1521);
}
.br-title {
  font-size: 0.9rem;
  font-weight: 600;
}
.br-depth {
  margin-left: auto;
  font-size: 0.72rem;
  color: var(--k-fg-muted, #94a3b8);
}
.br-body {
  overflow-y: auto;
  padding: 1rem 1.1rem;
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
.br-section {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}
.br-h {
  margin: 0;
  font-size: 0.78rem;
  font-weight: 600;
  color: var(--k-fg, #cbd5e1);
}
.br-replay-host {
  position: relative;
  background: var(--k-bg-deep, #06101b);
  border: 1px solid #16202e;
  border-radius: 6px;
  min-height: 260px;
  height: 320px;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
}
/* rrweb injects an absolutely-positioned .replayer-wrapper holding the iframe;
   center it within the host. */
.br-replay-host :deep(.replayer-wrapper) {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
}
.br-replay-host :deep(iframe) {
  border: none;
  background: #fff;
}
.br-replay-ctl {
  display: flex;
  align-items: center;
  gap: 0.6rem;
}
.br-scrub {
  flex: 1;
  accent-color: var(--k-fg-accent, #60a5fa);
}
.br-har {
  list-style: none;
  margin: 0;
  padding: 0;
  font-size: 0.72rem;
  font-family: ui-monospace, monospace;
  max-height: 9rem;
  overflow-y: auto;
}
.br-har-row {
  display: flex;
  gap: 0.5rem;
  padding: 0.15rem 0;
  border-bottom: 1px solid #11202f;
}
.br-method {
  color: var(--k-fg-accent, #60a5fa);
  flex-shrink: 0;
  width: 3.2rem;
}
.br-url {
  color: var(--k-fg, #cbd5e1);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  flex: 1;
}
.br-status {
  color: var(--k-fg-muted, #94a3b8);
  flex-shrink: 0;
}
.br-toggle {
  align-self: flex-start;
  background: none;
  border: none;
  color: var(--k-fg-accent, #60a5fa);
  cursor: pointer;
  font: inherit;
  font-size: 0.72rem;
  padding: 0.15rem 0;
}
.br-toggle:hover {
  text-decoration: underline;
}
.br-raw {
  background: var(--k-bg-deep, #06101b);
  border: 1px solid #16202e;
  border-radius: 6px;
  padding: 0.5rem;
  font-size: 0.68rem;
  max-height: 12rem;
  overflow: auto;
  margin: 0;
}
.br-console {
  list-style: none;
  margin: 0;
  padding: 0;
  font-size: 0.7rem;
  font-family: ui-monospace, monospace;
  max-height: 9rem;
  overflow-y: auto;
}
.br-console-row {
  display: flex;
  gap: 0.5rem;
  padding: 0.1rem 0;
}
.br-placement {
  display: grid;
  gap: 0.35rem;
  font-size: 0.74rem;
}
.br-placement div {
  display: grid;
  grid-template-columns: 5rem minmax(0, 1fr);
  gap: 0.5rem;
}
.br-placement dt {
  color: var(--k-fg-muted, #94a3b8);
}
.br-placement dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
}
.br-level {
  color: var(--k-warning, #f59e0b);
  flex-shrink: 0;
  width: 3rem;
}
.br-text {
  color: var(--k-fg, #cbd5e1);
}
.br-label {
  font-size: 0.74rem;
  color: var(--k-fg, #cbd5e1);
  margin-top: 0.3rem;
}
.br-input,
.br-textarea {
  background: var(--k-bg-input, #11243a);
  border: 1px solid #1e3a5f;
  border-radius: 6px;
  color: var(--k-fg, #e2e8f0);
  font: inherit;
  font-size: 0.8rem;
  padding: 0.45rem 0.6rem;
}
.br-textarea {
  resize: vertical;
}
.br-muted {
  color: var(--k-fg-muted, #64748b);
  font-size: 0.72rem;
  margin: 0;
}
.br-footer {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 0.6rem;
  padding: 0.75rem 1.1rem;
  border-top: 1px solid #1e3a5f;
  background: var(--k-bg-deep, #0a1521);
}
.br-cancel {
  background: none;
  border: 1px solid #334155;
  border-radius: 6px;
  color: var(--k-fg, #cbd5e1);
  cursor: pointer;
  font: inherit;
  font-size: 0.8rem;
  padding: 0.45rem 0.85rem;
}
.br-cancel:hover {
  border-color: #475569;
}
.br-submit {
  background: var(--k-button-bg, #2563eb);
  border: none;
  border-radius: 6px;
  color: var(--k-button-fg, #fff);
  font: inherit;
  font-size: 0.8rem;
  font-weight: 600;
  padding: 0.45rem 0.95rem;
  cursor: pointer;
}
.br-submit:hover:not(:disabled) {
  background: var(--k-button-hover-bg, #1d4ed8);
}
.br-submit:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
