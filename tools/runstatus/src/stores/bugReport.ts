/**
 * Bug-report capture + review state machine. Drives the review-before-file
 * flow: click → capture (rrweb session replay + console + errors + scrubbed HAR
 * preview) → operator reviews in a modal → submit (files the held capture) or
 * cancel (discards it).
 *
 * The visual evidence is the rrweb replay — a faithful reconstruction of the
 * exact DOM the operator saw (and any earlier frame, by scrubbing). We do NOT
 * rasterize a separate screenshot: html2canvas cannot render this app's styling
 * (modern CSS / custom properties) and produced a blank frame, while rrweb
 * captures the real DOM. The replay subsumes the screenshot primitive.
 *
 * The store holds only UI state; the live source and the capture functions are
 * injected at trigger time for testability.
 */

import { defineStore } from "pinia";
import { ref } from "vue";
import type {
  BugReportParams,
  BugReportResult,
  BugPreviewResult,
  Har,
  LiveSource,
} from "../data/live-source.js";
import { snapshotSessionEvents } from "../data/session-capture.js";
import { recentConsole } from "../data/console-capture.js";
import { gatherErrorInfo } from "../data/error-capture.js";
import { snapshotNetworkHar } from "../data/network-capture.js";

export type BugReportStatus =
  | "idle"
  | "capturing"
  | "reviewing"
  | "submitting"
  | "filed"
  | "error";

/** The live-source subset this store needs (eases injection in tests). */
export type BugSource = Pick<
  LiveSource,
  "bugPreview" | "reportBug" | "lastRpcError"
>;

/** Pluggable capture functions (defaults wired to the real modules). */
export interface CaptureDeps {
  snapshotEvents: () => unknown[];
  snapshotHar: () => unknown;
  recentConsole: () => Array<{ level: string; ts: unknown; text: string }>;
  gatherErrorInfo: (
    source?: BugSource
  ) => { errors: unknown[]; last_rpc: unknown };
}

const defaultDeps: CaptureDeps = {
  snapshotEvents: () => snapshotSessionEvents(),
  snapshotHar: () => snapshotNetworkHar(),
  recentConsole: () => recentConsole(10),
  gatherErrorInfo: (source) => gatherErrorInfo(source),
};

export interface TriggerOptions {
  source: BugSource;
  /** Default title for the modal (operator can edit). */
  defaultTitle?: string;
  /** Optional clicked DOM location for point-specific bug reports. */
  placement?: BugPlacementContext;
  /** trace_ref to attach (e.g. the current session id). */
  traceRef?: string;
  severity?: string;
  /** Override capture deps (tests inject stubs). */
  deps?: Partial<CaptureDeps>;
}

export interface BugPlacementContext {
  x: number;
  y: number;
  selector: string;
  text?: string;
  route?: string;
}

function formatPlacementContext(ctx: BugPlacementContext): string {
  const lines = [
    "Clicked location:",
    `- viewport: ${Math.round(ctx.x)}, ${Math.round(ctx.y)}`,
    `- target: ${ctx.selector}`,
  ];
  if (ctx.text) lines.push(`- text: ${ctx.text}`);
  if (ctx.route) lines.push(`- route: ${ctx.route}`);
  return lines.join("\n");
}

export const useBugReportStore = defineStore("bugReport", () => {
  const status = ref<BugReportStatus>("idle");
  const filed = ref<BugReportResult | null>(null);
  const error = ref<string>("");

  // Captured payload, stashed while reviewing.
  const har = ref<Har | null>(null);
  const depth = ref<number>(0);
  const rrwebEvents = ref<unknown[]>([]);
  const consoleLogs = ref<unknown[]>([]);
  const errorInfo = ref<unknown>(null);

  // Editable review fields.
  const title = ref<string>("");
  const description = ref<string>("");
  const placement = ref<BugPlacementContext | null>(null);

  // Stashed for submit().
  let captureId = "";
  let activeSource: BugSource | null = null;
  let activeSeverity: string | undefined;
  let activeTraceRef: string | undefined;

  function reset(): void {
    status.value = "idle";
    filed.value = null;
    error.value = "";
    har.value = null;
    depth.value = 0;
    rrwebEvents.value = [];
    consoleLogs.value = [];
    errorInfo.value = null;
    title.value = "";
    description.value = "";
    placement.value = null;
    captureId = "";
    activeSource = null;
    activeSeverity = undefined;
    activeTraceRef = undefined;
  }

  /**
   * Capture everything and move to the review state. Gathers the rrweb session
   * events, browser HAR, recent console, and error info, then calls
   * bugPreview() for a scrubbed held capture_id. On any failure → error.
   */
  async function trigger(opts: TriggerOptions): Promise<void> {
    const deps: CaptureDeps = { ...defaultDeps, ...opts.deps };
    filed.value = null;
    error.value = "";
    status.value = "capturing";
    activeSource = opts.source;
    activeSeverity = opts.severity;
    activeTraceRef = opts.traceRef;
    placement.value = opts.placement ?? null;
    try {
      // Local captures are best-effort; never let one block the preview.
      try {
        rrwebEvents.value = deps.snapshotEvents();
      } catch {
        rrwebEvents.value = [];
      }
      try {
        consoleLogs.value = deps.recentConsole();
      } catch {
        consoleLogs.value = [];
      }
      try {
        errorInfo.value = deps.gatherErrorInfo(opts.source);
      } catch {
        errorInfo.value = null;
      }

      let harJSON: string | undefined;
      try {
        const capturedHar = deps.snapshotHar();
        if (capturedHar) harJSON = JSON.stringify(capturedHar);
      } catch {
        harJSON = undefined;
      }

      const preview: BugPreviewResult = await opts.source.bugPreview({
        har_json: harJSON,
      });
      captureId = preview.capture_id;
      har.value = preview.har ?? null;
      depth.value = preview.depth ?? 0;

      title.value = opts.defaultTitle ?? "";
      description.value = opts.placement
        ? `${formatPlacementContext(opts.placement)}\n\nWhat should change here?\n`
        : "";
      status.value = "reviewing";
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e);
      status.value = "error";
    }
  }

  /** File the reviewed bug with the held capture and operator prose. */
  async function submit(): Promise<void> {
    if (status.value !== "reviewing" || !activeSource) return;
    status.value = "submitting";
    error.value = "";
    try {
      const params: BugReportParams = {
        capture_id: captureId || undefined,
        title: title.value || undefined,
        description: description.value || undefined,
        severity: activeSeverity,
        trace_ref: activeTraceRef,
        rrweb_events: rrwebEvents.value.length
          ? JSON.stringify(rrwebEvents.value)
          : undefined,
        console_logs: consoleLogs.value.length
          ? JSON.stringify(consoleLogs.value)
          : undefined,
        error_info: errorInfo.value
          ? JSON.stringify(errorInfo.value)
          : undefined,
      };
      const result = await activeSource.reportBug(params);
      filed.value = result;
      status.value = "filed";
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e);
      status.value = "error";
    }
  }

  /** Discard the in-flight review and return to idle. */
  function cancel(): void {
    reset();
  }

  return {
    status,
    filed,
    error,
    har,
    depth,
    rrwebEvents,
    consoleLogs,
    errorInfo,
    title,
    description,
    placement,
    reset,
    trigger,
    submit,
    cancel,
  };
});
