/**
 * BridgeTransport — the VS Code webview implementation of {@link RpcTransport}.
 *
 * The webview sandbox forbids the SPA document from opening cross-origin HTTP or
 * SSE to the spawned `kitsoki web`. So every wire op rides a `postMessage`
 * envelope to the extension host, which holds the only HTTP/SSE connection and
 * relays JSON-RPC replies and SSE frames back. The host owns reconnect/backfill;
 * this side never backs off (a closed stream is the host's to revive).
 *
 * Envelope protocol (webview → host, then host → webview). The discriminant is
 * `t`; it MUST match the host relay (tools/vscode-kitsoki/src/relay.ts) byte for
 * byte. See .context/vscode-extension-build-contract.md.
 *
 *   call        { t:"call", id, method, params }
 *               ← { t:"call-ok", id, result }
 *               ← { t:"call-err", id, error:{ code?, message, data? } }
 *
 *   evt-open    { t:"evt-open", id, path, query }
 *               ← { t:"evt-msg", id, data }         (one per SSE frame — RAW
 *                                                     `data:` string; parsed above)
 *               ← { t:"evt-err", id }               (surfaced; host reconnects)
 *   evt-close   { t:"evt-close", id }               (on unsubscribe)
 *
 *   post-open   { t:"post-open", id, path, body }
 *               ← { t:"post-frame", id, frame }     (one per SSE frame, parsed)
 *               ← { t:"post-done", id, frame }      (terminal — reduce + resolve)
 *               ← { t:"post-err", id, error:{ message } }   (reject)
 *
 * `id` is a monotonic counter minted here; the host echoes it on every reply so
 * this side can correlate the pending call / open stream / pending post.
 */

import type {
  RpcTransport,
  LastRpcError,
  EventStreamOptions,
  PostEventStreamHandlers,
} from "./transport.js";
import { JsonRpcError } from "./jsonrpc.js";

/** The minimal VS Code webview API surface this transport uses. */
export interface VsCodeApiLike {
  postMessage(message: unknown): void;
}

/** A host → webview reply envelope. `id` correlates to the originating op. */
interface HostMessage {
  t: string;
  id: number;
  // call-ok
  result?: unknown;
  // call-err / post-err / evt-err — the error rides nested (mirrors relay.ts).
  error?: { code?: number; message: string; data?: unknown };
  // evt-msg — the RAW SSE `data` payload string (the data layer JSON.parses it).
  data?: string;
  // post-frame / post-done — the parsed frame object.
  frame?: Record<string, unknown>;
}

interface PendingCall {
  resolve: (v: unknown) => void;
  reject: (e: unknown) => void;
  method: string;
}

interface OpenStream {
  opts: EventStreamOptions;
}

interface PendingPost {
  handlers: PostEventStreamHandlers<unknown>;
  resolve: (v: unknown) => void;
  reject: (e: unknown) => void;
}

export class BridgeTransport implements RpcTransport {
  private readonly api: VsCodeApiLike;
  private lastError: LastRpcError | null = null;

  private readonly calls = new Map<number, PendingCall>();
  private readonly streams = new Map<number, OpenStream>();
  private readonly posts = new Map<number, PendingPost>();

  // Monotonic id space shared across call / evt / post so a host can correlate
  // any reply by id alone.
  private nextId = 1;

  /**
   * @param api    the VS Code webview api (defaults to `acquireVsCodeApi()`).
   * @param target the event target to listen on (defaults to `window`).
   */
  constructor(api?: VsCodeApiLike, target: EventTarget = window) {
    this.api = api ?? acquireVsCodeApi();
    target.addEventListener("message", (ev) =>
      this.onHostMessage((ev as MessageEvent).data as HostMessage)
    );
  }

  getLastError(): LastRpcError | null {
    return this.lastError;
  }

  call<T = unknown>(
    method: string,
    params: Record<string, unknown>,
    _id: number
  ): Promise<T> {
    // Ignore the caller-supplied id. As the shared singleton transport this
    // instance mints ALL wire ids from one monotonic space — each JsonRpcClient
    // has its own nextId starting at 1, so honoring theirs would let two clients
    // both send call id=1 and cross-resolve each other's reply. The host echoes
    // whatever id we send, so a private id keeps correlation unambiguous.
    const id = this.nextId++;
    return new Promise<T>((resolve, reject) => {
      this.calls.set(id, {
        resolve: resolve as (v: unknown) => void,
        reject,
        method,
      });
      this.api.postMessage({ t: "call", id, method, params });
    });
  }

  openEventStream(
    path: string,
    query: Record<string, string>,
    opts: EventStreamOptions
  ): () => void {
    const id = this.nextId++;
    this.streams.set(id, { opts });
    this.api.postMessage({ t: "evt-open", id, path, query });

    return () => {
      if (this.streams.delete(id)) {
        this.api.postMessage({ t: "evt-close", id });
      }
    };
  }

  postEventStream<TResult>(
    path: string,
    body: Record<string, unknown>,
    handlers: PostEventStreamHandlers<TResult>
  ): Promise<TResult> {
    const id = this.nextId++;
    return new Promise<TResult>((resolve, reject) => {
      this.posts.set(id, {
        handlers: handlers as PostEventStreamHandlers<unknown>,
        resolve: resolve as (v: unknown) => void,
        reject,
      });
      this.api.postMessage({ t: "post-open", id, path, body });
    });
  }

  private onHostMessage(msg: HostMessage): void {
    if (!msg || typeof msg.t !== "string") return;

    switch (msg.t) {
      case "call-ok": {
        const pending = this.calls.get(msg.id);
        if (!pending) return;
        this.calls.delete(msg.id);
        pending.resolve(msg.result);
        return;
      }
      case "call-err": {
        const pending = this.calls.get(msg.id);
        if (!pending) return;
        this.calls.delete(msg.id);
        const code = msg.error?.code ?? -1;
        const message = msg.error?.message ?? "bridge call error";
        this.lastError = { method: pending.method, code, message };
        pending.reject(new JsonRpcError(code, message, msg.error?.data));
        return;
      }
      case "evt-msg": {
        const stream = this.streams.get(msg.id);
        if (!stream) return;
        if (typeof msg.data === "string") stream.opts.onMessage(msg.data);
        return;
      }
      case "evt-err": {
        const stream = this.streams.get(msg.id);
        if (!stream) return;
        // Host owns reconnect — surface only; do not back off here.
        stream.opts.onError?.(new Error(msg.error?.message ?? "bridge stream error"));
        return;
      }
      case "post-frame": {
        const pending = this.posts.get(msg.id);
        if (!pending || !msg.frame) return;
        pending.handlers.onFrame(msg.frame);
        return;
      }
      case "post-done": {
        const pending = this.posts.get(msg.id);
        if (!pending) return;
        this.posts.delete(msg.id);
        const frame = msg.frame ?? {};
        try {
          const terminal = pending.handlers.reduce(frame);
          if (terminal !== undefined) {
            pending.resolve(terminal.result);
          } else {
            pending.reject(
              new Error("bridge post stream ended without terminal frame")
            );
          }
        } catch (e) {
          pending.reject(e);
        }
        return;
      }
      case "post-err": {
        const pending = this.posts.get(msg.id);
        if (!pending) return;
        this.posts.delete(msg.id);
        pending.reject(new Error(msg.error?.message ?? "bridge post error"));
        return;
      }
      default:
        return;
    }
  }
}
