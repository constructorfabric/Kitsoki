/**
 * RpcTransport — the DI seam between the runstatus SPA's data layer and the
 * wire.
 *
 * Two implementations satisfy this interface:
 *
 *   - HttpTransport (this file): the production browser transport. POSTs to
 *     `/rpc`, opens EventSource streams against `/rpc/events`/`/rpc/notifications`
 *     /`/rpc/questions`, and reads POST-then-SSE bodies for the turn/meta
 *     streams. Owns the reconnect/backfill/backoff lifecycle.
 *
 *   - BridgeTransport (./bridge-transport): the VS Code webview transport. Every
 *     wire op rides a `postMessage` envelope to the extension host, which holds
 *     the only HTTP/SSE connection to the spawned `kitsoki web`. The host owns
 *     reconnect, so the webview side never backs off.
 *
 * The factory `createTransport(base)` picks BridgeTransport when running inside a
 * VS Code webview (`acquireVsCodeApi` is present) and HttpTransport otherwise, so
 * every existing `new JsonRpcClient(base)` / `new LiveSource(base)` call site
 * transparently bridges in the editor with no store or component change.
 */

import type { JsonRpcRequest, JsonRpcResponse } from "./jsonrpc.js";
import { JsonRpcError } from "./jsonrpc.js";

/** The last failed RPC, surfaced for bug-report error context. */
export interface LastRpcError {
  method: string;
  code: unknown;
  message: string;
}

/**
 * Options for an EventSource-style subscription opened via
 * {@link RpcTransport.openEventStream}. The transport owns the EventSource
 * lifecycle (open → message → error → reconnect); the caller supplies the
 * per-frame handler and (optionally) a backfill hook run before each reopen.
 */
export interface EventStreamOptions {
  /** Called for every raw SSE frame's `data` payload. */
  onMessage: (data: string) => void;
  /** Called once per transport error (before reconnect), for surfacing. */
  onError?: (e: unknown) => void;
  /**
   * Optional backfill hook run after an error, before the stream is reopened.
   * Resolves when backfill is complete; the stream reopens regardless of
   * success/failure (mirrors the per-session reconnect→getTrace→reopen path).
   */
  onReconnect?: () => Promise<void>;
}

/**
 * Handler set for a POST-then-SSE stream opened via
 * {@link RpcTransport.postEventStream}. The transport POSTs the body, then reads
 * the streamed `data:` frames; `onFrame` receives each frame's parsed JSON until
 * the promise resolves (a terminal frame) or rejects (error / network failure).
 */
export interface PostEventStreamHandlers<TResult> {
  /** Called for each non-terminal frame (think/delta/tool). */
  onFrame: (frame: Record<string, unknown>) => void;
  /**
   * Maps a parsed frame to a terminal outcome: return `{ done: TResult }` to
   * resolve, throw to reject, or return `undefined` to forward to `onFrame`.
   */
  reduce: (frame: Record<string, unknown>) => { result: TResult } | undefined;
}

/**
 * The transport seam. Implementations carry NO knowledge of method semantics —
 * they move bytes. The JSON-RPC method set, SSE framing, and reduce/backfill
 * policy all live in the data layer above (jsonrpc.ts / live-source.ts).
 */
export interface RpcTransport {
  /** The most recent failed RPC (HTTP or JSON-RPC error), or null. */
  getLastError(): LastRpcError | null;

  /** One request/response RPC. Rejects with JsonRpcError on a JSON-RPC error. */
  call<T = unknown>(method: string, params: Record<string, unknown>, id: number): Promise<T>;

  /**
   * Open a long-lived EventSource-style stream against `path` (e.g.
   * "rpc/events") with `query` params. Returns an unsubscribe function. The
   * transport owns reconnect/backoff; the HTTP impl backs off and reopens, the
   * bridge impl defers reconnect to the host.
   */
  openEventStream(
    path: string,
    query: Record<string, string>,
    opts: EventStreamOptions
  ): () => void;

  /**
   * POST `body` to `path` (e.g. "rpc/turn-stream") and read the streamed SSE
   * frames, resolving with the reduced terminal result.
   */
  postEventStream<TResult>(
    path: string,
    body: Record<string, unknown>,
    handlers: PostEventStreamHandlers<TResult>
  ): Promise<TResult>;
}

// Backoff schedule: 250, 500, 1000, 2000, then cap at 5000 ms.
const BACKOFF_MS = [250, 500, 1000, 2000, 5000];

function nextBackoff(attempt: number): number {
  return BACKOFF_MS[Math.min(attempt, BACKOFF_MS.length - 1)] ?? 5000;
}

/**
 * HttpTransport — the production browser transport. Holds the EXACT fetch /
 * EventSource bodies that previously lived inline in JsonRpcClient.post /
 * .subscribe and LiveSource.turnStream / .metaStream / .subscribeNotifications /
 * .subscribeQuestions, with the reconnect/backfill/backoff preserved verbatim.
 */
export class HttpTransport implements RpcTransport {
  private readonly base: string;
  private lastError: LastRpcError | null = null;

  constructor(base = "/") {
    // Normalise: ensure it ends with "/"
    this.base = base.endsWith("/") ? base : base + "/";
  }

  getLastError(): LastRpcError | null {
    return this.lastError;
  }

  async call<T = unknown>(
    method: string,
    params: Record<string, unknown>,
    id: number
  ): Promise<T> {
    const body: JsonRpcRequest = { jsonrpc: "2.0", id, method, params };

    let resp: Response;
    try {
      resp = await fetch(`${this.base}rpc`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e);
      this.lastError = { method, code: "fetch", message };
      throw e;
    }

    if (!resp.ok) {
      this.lastError = {
        method,
        code: resp.status,
        message: `HTTP ${resp.status}: ${resp.statusText}`,
      };
      throw new JsonRpcError(
        resp.status,
        `HTTP ${resp.status}: ${resp.statusText}`
      );
    }

    const frame = (await resp.json()) as JsonRpcResponse<T>;

    if (frame.error !== undefined) {
      this.lastError = {
        method,
        code: frame.error.code,
        message: frame.error.message,
      };
      throw new JsonRpcError(
        frame.error.code,
        frame.error.message,
        frame.error.data
      );
    }

    return frame.result as T;
  }

  openEventStream(
    path: string,
    query: Record<string, string>,
    opts: EventStreamOptions
  ): () => void {
    let es: EventSource | null = null;
    let closed = false;
    let backoffAttempt = 0;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

    const qs = Object.entries(query)
      .map(([k, v]) => `${k}=${encodeURIComponent(v)}`)
      .join("&");

    const openStream = () => {
      if (closed) return;

      const url = qs
        ? `${this.base}${path}?${qs}`
        : `${this.base}${path}`;
      es = new EventSource(url);

      es.onmessage = (ev: MessageEvent<string>) => {
        backoffAttempt = 0; // reset on successful message
        opts.onMessage(ev.data);
      };

      es.onerror = (e) => {
        if (closed) return;
        opts.onError?.(e);
        es?.close();
        es = null;

        const delay = nextBackoff(backoffAttempt++);
        reconnectTimer = setTimeout(() => {
          if (closed) return;
          if (opts.onReconnect) {
            opts
              .onReconnect()
              .then(() => {
                if (!closed) openStream();
              })
              .catch(() => {
                if (!closed) openStream();
              });
          } else {
            openStream();
          }
        }, delay);
      };
    };

    openStream();

    return () => {
      closed = true;
      if (reconnectTimer !== null) {
        clearTimeout(reconnectTimer);
      }
      es?.close();
      es = null;
    };
  }

  async postEventStream<TResult>(
    path: string,
    body: Record<string, unknown>,
    handlers: PostEventStreamHandlers<TResult>
  ): Promise<TResult> {
    const resp = await fetch(`${this.base}${path}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!resp.ok) {
      throw new Error(`${path}: HTTP ${resp.status} ${resp.statusText}`);
    }
    if (!resp.body) {
      throw new Error(`${path}: no response body`);
    }

    const reader = resp.body.getReader();
    const dec = new TextDecoder();
    let buf = "";
    let finalResult: { result: TResult } | null = null;

    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      const lines = buf.split("\n");
      buf = lines.pop() ?? "";
      for (const line of lines) {
        if (!line.startsWith("data: ")) continue;
        const raw = line.slice(6).trim();
        if (!raw) continue;
        const frame = JSON.parse(raw) as Record<string, unknown>;
        const terminal = handlers.reduce(frame);
        if (terminal !== undefined) {
          finalResult = terminal;
        } else {
          handlers.onFrame(frame);
        }
      }
    }

    if (!finalResult) throw new Error(`${path}: ended without done event`);
    return finalResult.result;
  }
}

/**
 * Choose the transport for the current host: BridgeTransport inside a VS Code
 * webview (where `acquireVsCodeApi` is injected by the host), HttpTransport in a
 * plain browser tab. The import of BridgeTransport is lazy-free (top-level) but
 * only instantiated in the webview branch.
 */
export function createTransport(base = "/"): RpcTransport {
  if (typeof acquireVsCodeApi === "function") {
    // The SPA constructs ~15 LiveSource instances (App.vue + each store +
    // overlay), so createTransport runs many times in the webview. BridgeTransport
    // MUST be a process singleton: acquireVsCodeApi() throws if called twice, and
    // a shared instance also gives ONE postMessage listener and ONE monotonic id
    // space so the host relay never sees colliding ids from two transports.
    return getSharedBridgeTransport();
  }
  return new HttpTransport(base);
}

/** The one BridgeTransport for this webview document (see createTransport). */
let sharedBridge: BridgeTransport | undefined;

function getSharedBridgeTransport(): BridgeTransport {
  if (!sharedBridge) sharedBridge = new BridgeTransport();
  return sharedBridge;
}

// Imported after createTransport's declaration-order is irrelevant for runtime;
// kept at the bottom to keep the production transport (HttpTransport) the focus
// of this module.
import { BridgeTransport } from "./bridge-transport.js";
