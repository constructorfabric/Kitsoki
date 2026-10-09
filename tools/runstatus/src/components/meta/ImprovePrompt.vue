<template>
  <section
    class="improve-prompt"
    data-testid="improve-prompt"
    aria-live="polite"
  >
    <div class="improve-prompt__copy">
      <div class="improve-prompt__title">Improve this run</div>
      <div class="improve-prompt__body">
        Review the completed session for false starts, wasted tool calls,
        prompt/tool changes, scripts, permission cleanup, and regression
        coverage.
      </div>
      <div
        v-if="statusMessage"
        class="improve-prompt__status"
        data-testid="improve-status"
      >
        {{ statusMessage }}
      </div>
      <div
        v-if="error"
        class="improve-prompt__error"
        data-testid="improve-error"
      >
        {{ error }}
      </div>
      <div
        v-if="reportResult"
        class="improve-prompt__report"
        data-testid="improve-report-status"
      >
        <div>
          Evidence report filed to {{ reportSinkLabel }}.
          <a
            v-if="reportResult.url"
            :href="reportResult.url"
            target="_blank"
            rel="noreferrer"
            data-testid="improve-report-url"
          >
            Open ticket
          </a>
          <span
            v-else-if="reportPrimaryPath"
            data-testid="improve-report-path"
          >
            {{ reportPrimaryPath }}
          </span>
        </div>
        <div
          v-if="reportArtifacts.length"
          class="improve-prompt__artifacts"
          data-testid="improve-report-artifacts"
        >
          Artifacts: {{ reportArtifacts.join(", ") }}
        </div>
        <div v-if="reportResult.provider_error" class="improve-prompt__error">
          Provider post failed; local evidence is still filed.
          {{ reportResult.provider_error }}
        </div>
      </div>
    </div>

    <div class="improve-prompt__actions">
      <button
        type="button"
        class="improve-prompt__run"
        data-testid="improve-run"
        :disabled="runDisabled"
        :title="runTitle"
        @click="runImprove()"
      >
        {{ runLabel }}
      </button>

      <label class="improve-prompt__auto">
        <input
          v-model="autoRun"
          type="checkbox"
          data-testid="improve-auto-toggle"
        />
        <span>Auto-run at completion</span>
      </label>

      <label class="improve-prompt__auto">
        <input
          v-model="fileEvidenceReport"
          type="checkbox"
          data-testid="improve-report-toggle"
        />
        <span>File evidence report</span>
      </label>

      <select
        v-model="reportDestination"
        class="improve-prompt__select"
        data-testid="improve-report-destination"
        :disabled="!fileEvidenceReport || runState !== 'idle'"
        aria-label="Improve report destination"
      >
        <option value="configured">Configured sink</option>
        <option value="local">Local artifact</option>
      </select>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import {
  LiveSource,
  type MetaImproveReportResult,
} from "../../data/live-source.js";
import { useMetaStore } from "../../stores/meta.js";
import { recentConsole } from "../../data/console-capture.js";
import { gatherErrorInfo } from "../../data/error-capture.js";
import { snapshotNetworkHar } from "../../data/network-capture.js";
import { snapshotSessionEvents } from "../../data/session-capture.js";

const IMPROVE_MODE = "story.improve";
const AUTO_RUN_KEY = "kitsoki:improve:autoRun";
const AUTO_RAN_PREFIX = "kitsoki:improve:autoRan:";
const FILE_REPORT_KEY = "kitsoki:improve:fileReport";
const REPORT_DESTINATION_KEY = "kitsoki:improve:reportDestination";
const IMPROVE_PROMPT =
  "Review this completed session for false starts, unexpected output, wasted tool calls, prompt/tool/script improvements, permission cleanup, and no-LLM regression coverage. Produce the standard introspection improvement report, including evidence/posting recommendations for the follow-up report.";

const props = defineProps<{ sessionId: string }>();

const meta = useMetaStore();
const source = new LiveSource("/");
const autoRun = ref(readAutoRun());
const fileEvidenceReport = ref(readFileReport());
const reportDestination = ref<"configured" | "local">(readReportDestination());
const modesLoadedFor = ref("");
const loadingModes = ref(false);
const runState = ref<"idle" | "starting" | "running" | "filing">("idle");
const statusMessage = ref("");
const error = ref("");
const reportResult = ref<MetaImproveReportResult | null>(null);

const improveAvailable = computed(
  () =>
    modesLoadedFor.value === props.sessionId &&
    meta.modes.some((m) => m.key === IMPROVE_MODE)
);
const improveBusy = computed(
  () => meta.statusFor(props.sessionId, IMPROVE_MODE).busy
);
const runDisabled = computed(
  () =>
    !props.sessionId ||
    loadingModes.value ||
    runState.value !== "idle" ||
    !improveAvailable.value ||
    improveBusy.value
);
const runLabel = computed(() => {
  if (runState.value === "starting") return "Opening improve...";
  if (runState.value === "running" || improveBusy.value) return "Running improve...";
  if (runState.value === "filing") return "Filing evidence...";
  if (loadingModes.value) return "Checking improve...";
  if (!improveAvailable.value) return "Improve unavailable";
  return "Run improve now";
});
const runTitle = computed(() =>
  improveAvailable.value
    ? "Open Meta, run the completed-session improvement report, and file evidence when enabled"
    : "This session does not advertise story.improve"
);
const reportArtifacts = computed(() => reportResult.value?.artifacts ?? []);
const reportPrimaryPath = computed(
  () =>
    reportResult.value?.path ??
    reportResult.value?.local_path ??
    reportResult.value?.artifacts_path ??
    reportResult.value?.local_artifacts_path ??
    ""
);
const reportSinkLabel = computed(() => {
  const sink = reportResult.value?.sink ?? "";
  if (sink === "ticket-provider") return "ticket provider";
  if (sink === "github") return "GitHub";
  return "local artifacts";
});

onMounted(() => {
  void refreshModes().then(maybeAutoRun);
});

watch(
  () => props.sessionId,
  () => {
    statusMessage.value = "";
    error.value = "";
    reportResult.value = null;
    void refreshModes().then(maybeAutoRun);
  }
);

watch(autoRun, (enabled) => {
  writeAutoRun(enabled);
  if (enabled) void maybeAutoRun();
});

watch(fileEvidenceReport, (enabled) => writeFileReport(enabled));
watch(reportDestination, (destination) => writeReportDestination(destination));

async function refreshModes(): Promise<void> {
  if (!props.sessionId) return;
  loadingModes.value = true;
  error.value = "";
  meta.setSession(props.sessionId);
  try {
    await meta.loadModes(source, props.sessionId);
    modesLoadedFor.value = props.sessionId;
  } finally {
    loadingModes.value = false;
  }
}

async function runImprove(): Promise<void> {
  if (!props.sessionId || runState.value !== "idle" || improveBusy.value) return;
  statusMessage.value = "";
  error.value = "";
  reportResult.value = null;
  if (!improveAvailable.value) await refreshModes();
  if (!improveAvailable.value) {
    error.value = "Improve mode is not available for this session.";
    return;
  }
  runState.value = "starting";
  try {
    await meta.openMode(source, props.sessionId, IMPROVE_MODE);
    runState.value = "running";
    await meta.send(source, IMPROVE_PROMPT);
    if (meta.error) {
      error.value = meta.error;
      return;
    }
    const report = latestAssistantReport();
    if (fileEvidenceReport.value) {
      runState.value = "filing";
      reportResult.value = await fileImproveReport(report);
      statusMessage.value = reportResult.value.provider_error
        ? "Improve report is ready in Meta; local evidence filed, remote provider needs attention."
        : "Improve report is ready in Meta and evidence report is filed.";
    } else {
      statusMessage.value = "Improve report is ready in Meta.";
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    runState.value = "idle";
  }
}

async function maybeAutoRun(): Promise<void> {
  if (!autoRun.value || !props.sessionId || !improveAvailable.value) return;
  if (!claimAutoRun(props.sessionId)) return;
  await runImprove();
}

async function fileImproveReport(
  report: string
): Promise<MetaImproveReportResult> {
  const rrwebEvents = safeJSONStringify(snapshotSessionEvents());
  const consoleLogs = safeJSONStringify(recentConsole(10));
  const errorInfo = safeJSONStringify(gatherErrorInfo(source));
  let captureId = "";
  try {
    const preview = await source.bugPreview({
      har_json: safeJSONStringify(snapshotNetworkHar()),
    });
    captureId = preview.capture_id;
  } catch {
    captureId = "";
  }
  return source.metaImproveReport({
    session_id: props.sessionId,
    mode: IMPROVE_MODE,
    title: `introspection: improve report for ${props.sessionId}`,
    report,
    guidance: IMPROVE_PROMPT,
    destination: reportDestination.value,
    capture_id: captureId || undefined,
    trace_ref: props.sessionId,
    rrweb_events: rrwebEvents,
    console_logs: consoleLogs,
    error_info: errorInfo,
  });
}

function latestAssistantReport(): string {
  const transcript = meta.activeTranscript;
  for (let i = transcript.length - 1; i >= 0; i--) {
    const msg = transcript[i];
    if (msg.role === "assistant" && msg.text.trim()) return msg.text;
  }
  return "Improve report completed; see the meta transcript for the assistant response.";
}

function safeJSONStringify(value: unknown): string | undefined {
  try {
    return JSON.stringify(value);
  } catch {
    return undefined;
  }
}

function autoRanKey(sessionId: string): string {
  return `${AUTO_RAN_PREFIX}${sessionId}`;
}

function claimAutoRun(sessionId: string): boolean {
  try {
    const key = autoRanKey(sessionId);
    if (localStorage.getItem(key)) return false;
    localStorage.setItem(key, new Date().toISOString());
    return true;
  } catch {
    return false;
  }
}

function readAutoRun(): boolean {
  try {
    return localStorage.getItem(AUTO_RUN_KEY) === "1";
  } catch {
    return false;
  }
}

function readFileReport(): boolean {
  try {
    const raw = localStorage.getItem(FILE_REPORT_KEY);
    return raw == null ? true : raw === "1";
  } catch {
    return true;
  }
}

function writeFileReport(enabled: boolean): void {
  try {
    localStorage.setItem(FILE_REPORT_KEY, enabled ? "1" : "0");
  } catch {
    // Local storage is a convenience, not required for manual improve.
  }
}

function readReportDestination(): "configured" | "local" {
  try {
    return localStorage.getItem(REPORT_DESTINATION_KEY) === "local"
      ? "local"
      : "configured";
  } catch {
    return "configured";
  }
}

function writeReportDestination(destination: "configured" | "local"): void {
  try {
    localStorage.setItem(REPORT_DESTINATION_KEY, destination);
  } catch {
    // Local storage is a convenience, not required for manual improve.
  }
}

function writeAutoRun(enabled: boolean): void {
  try {
    if (enabled) localStorage.setItem(AUTO_RUN_KEY, "1");
    else localStorage.removeItem(AUTO_RUN_KEY);
  } catch {
    // Local storage is a convenience, not required for manual improve.
  }
}
</script>

<style scoped>
.improve-prompt {
  display: flex;
  align-items: stretch;
  flex-direction: column;
  justify-content: space-between;
  gap: 0.65rem;
  padding: 0.65rem 0.9rem;
  background: #0b1d2a;
  border-top: 1px solid #164e63;
  border-bottom: 1px solid #164e63;
  color: #dbeafe;
  flex-shrink: 0;
}

.improve-prompt__copy {
  min-width: 0;
  width: 100%;
}

.improve-prompt__title {
  font-size: 0.78rem;
  font-weight: 800;
  color: #bae6fd;
  letter-spacing: 0;
  text-transform: uppercase;
}

.improve-prompt__body {
  margin-top: 0.15rem;
  color: #cbd5e1;
  font-size: 0.82rem;
  line-height: 1.35;
}

.improve-prompt__status {
  margin-top: 0.25rem;
  color: #a7f3d0;
  font-size: 0.76rem;
}

.improve-prompt__error {
  margin-top: 0.25rem;
  color: #fecaca;
  font-size: 0.76rem;
}

.improve-prompt__report {
  margin-top: 0.3rem;
  color: #bbf7d0;
  font-size: 0.76rem;
  line-height: 1.35;
  overflow-wrap: anywhere;
}

.improve-prompt__report a,
.improve-prompt__report span {
  color: #7dd3fc;
  font-weight: 700;
}

.improve-prompt__artifacts {
  color: #cbd5e1;
}

.improve-prompt__actions {
  display: flex;
  align-items: center;
  gap: 0.65rem;
  flex-shrink: 0;
  flex-wrap: wrap;
  justify-content: flex-start;
  width: 100%;
}

.improve-prompt__run {
  border: 1px solid #38bdf8;
  border-radius: 4px;
  background: #075985;
  color: #f0f9ff;
  font: inherit;
  font-size: 0.8rem;
  font-weight: 700;
  padding: 0.34rem 0.7rem;
  cursor: pointer;
  white-space: nowrap;
}

.improve-prompt__run:hover:not(:disabled) {
  background: #0369a1;
  border-color: #7dd3fc;
}

.improve-prompt__run:disabled {
  cursor: not-allowed;
  opacity: 0.58;
}

.improve-prompt__auto {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  color: #bfdbfe;
  font-size: 0.78rem;
  white-space: nowrap;
}

.improve-prompt__auto input {
  width: 0.95rem;
  height: 0.95rem;
  accent-color: #38bdf8;
}

.improve-prompt__select {
  border: 1px solid #1d4ed8;
  border-radius: 4px;
  background: #0f172a;
  color: #dbeafe;
  font: inherit;
  font-size: 0.78rem;
  padding: 0.28rem 0.45rem;
}

.improve-prompt__select:disabled {
  opacity: 0.58;
}

@media (max-width: 720px) {
  .improve-prompt__actions {
    justify-content: space-between;
  }
}
</style>
