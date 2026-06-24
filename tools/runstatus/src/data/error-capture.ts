/**
 * Error capture: registers window.onerror + window.onunhandledrejection and
 * exposes a Vue errorHandler, all feeding a bounded buffer of error records.
 * gatherErrorInfo() combines the buffer with the last failed RPC (surfaced by
 * the live source) into the {errors, last_rpc} shape the backend expects.
 *
 * Everything is guarded so capture can never throw into the app.
 */

export interface ErrorEntry {
  ts: number;
  message: string;
  stack?: string;
  kind: "window" | "unhandledrejection" | "vue";
}

export interface LastRpcError {
  method: string;
  code: unknown;
  message: string;
}

/** A minimal window surface we attach handlers to. */
export interface ErrorWindow {
  onerror: OnErrorEventHandler | null;
  onunhandledrejection:
    | ((this: Window, ev: PromiseRejectionEvent) => unknown)
    | null;
}

/** Subset of LiveSource needed to surface the last failed RPC. */
export interface LastRpcSource {
  lastRpcError(): LastRpcError | null;
}

const RING_CAPACITY = 100;
const ring: ErrorEntry[] = [];

function record(entry: ErrorEntry): void {
  try {
    ring.push(entry);
    if (ring.length > RING_CAPACITY) ring.splice(0, ring.length - RING_CAPACITY);
  } catch {
    /* never throw */
  }
}

function messageOf(err: unknown): { message: string; stack?: string } {
  if (err instanceof Error) {
    return { message: err.message, stack: err.stack };
  }
  if (typeof err === "string") return { message: err };
  try {
    return { message: JSON.stringify(err) };
  } catch {
    return { message: String(err) };
  }
}

let installed = false;

/**
 * Register window.onerror + window.onunhandledrejection on the given window
 * (defaults to the global). Chains any pre-existing handlers. Idempotent for
 * the global window.
 */
export function installErrorCapture(win: ErrorWindow = window): void {
  const isGlobal =
    typeof window !== "undefined" && win === (window as ErrorWindow);
  if (isGlobal && installed) return;
  if (isGlobal) installed = true;

  const priorOnError = win.onerror;
  win.onerror = function (
    this: Window,
    message: Event | string,
    source?: string,
    lineno?: number,
    colno?: number,
    error?: Error
  ): boolean {
    const info = error ? messageOf(error) : { message: String(message) };
    record({ ts: Date.now(), kind: "window", ...info });
    if (typeof priorOnError === "function") {
      return (
        priorOnError.call(this, message, source, lineno, colno, error) ?? false
      );
    }
    return false;
  };

  const priorOnRejection = win.onunhandledrejection;
  win.onunhandledrejection = function (
    this: Window,
    ev: PromiseRejectionEvent
  ): unknown {
    record({ ts: Date.now(), kind: "unhandledrejection", ...messageOf(ev.reason) });
    if (typeof priorOnRejection === "function") {
      return priorOnRejection.call(this, ev);
    }
    return undefined;
  };
}

/**
 * Vue app.config.errorHandler — record component errors into the buffer.
 * Re-throws nothing; logs to console so the default surfacing is preserved.
 */
export function vueErrorHandler(
  err: unknown,
  _instance: unknown,
  info: string
): void {
  const m = messageOf(err);
  record({
    ts: Date.now(),
    kind: "vue",
    message: m.message + (info ? ` (${info})` : ""),
    stack: m.stack,
  });
  // Keep the default-ish behavior visible in dev.
  console.error("[vue]", err);
}

/** Return the most recent error entries (oldest-first). */
export function recentErrors(): ErrorEntry[] {
  return ring.slice();
}

/**
 * Build the {errors, last_rpc} payload for a bug report. last_rpc is the live
 * source's last failed RPC if any, else null.
 */
export function gatherErrorInfo(source?: LastRpcSource): {
  errors: ErrorEntry[];
  last_rpc: LastRpcError | null;
} {
  let lastRpc: LastRpcError | null = null;
  try {
    lastRpc = source?.lastRpcError() ?? null;
  } catch {
    lastRpc = null;
  }
  return { errors: recentErrors(), last_rpc: lastRpc };
}

/** Test helper: clear the ring buffer. */
export function __resetErrorCapture(): void {
  ring.length = 0;
  installed = false;
}
