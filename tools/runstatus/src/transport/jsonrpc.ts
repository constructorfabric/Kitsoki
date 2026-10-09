/**
 * JSON-RPC 2.0 transport for the kitsoki runstatus HTTP endpoint.
 *
 * - POST /rpc  — control (request/response)
 * - GET  /rpc/events?subscription_id=<id>  — text/event-stream notifications
 */

export interface JsonRpcRequest {
  jsonrpc: "2.0";
  id: number;
  method: string;
  params: Record<string, unknown>;
}

export interface JsonRpcResponse<T = unknown> {
  jsonrpc: "2.0";
  id: number;
  result?: T;
  error?: {
    code: number;
    message: string;
    data?: unknown;
  };
}

export interface JsonRpcNotification {
  jsonrpc: "2.0";
  method: string;
  params: Record<string, unknown>;
}

export class JsonRpcError extends Error {
  readonly code: number;
  readonly data: unknown;

  constructor(code: number, message: string, data?: unknown) {
    super(message);
    this.name = "JsonRpcError";
    this.code = code;
    this.data = data;
  }
}

import type { RpcTransport, LastRpcError } from "./transport.js";
import { HttpTransport } from "./transport.js";

/** The last failed RPC, surfaced for bug-report error context. */
export type { LastRpcError };

/**
 * VS Code injects `acquireVsCodeApi` into the webview's global scope. Declared
 * here so the transport factory's host-detection branch typechecks in the SPA
 * build (the function is absent in a plain browser tab — guard with `typeof`).
 */
declare global {
  // eslint-disable-next-line no-var
  function acquireVsCodeApi(): { postMessage(message: unknown): void };
}

export class JsonRpcClient {
  private readonly transport: RpcTransport;
  private nextId = 1;

  /**
   * @param base      JSON-RPC endpoint base (default "/"); used only when no
   *                  transport is injected (constructs an HttpTransport).
   * @param transport optional injected transport (HttpTransport in a browser
   *                  tab, BridgeTransport in a VS Code webview). DI seam.
   */
  constructor(base = "/", transport?: RpcTransport) {
    this.transport = transport ?? new HttpTransport(base);
  }

  /** The most recent failed RPC (HTTP or JSON-RPC error), or null. */
  getLastError(): LastRpcError | null {
    return this.transport.getLastError();
  }

  async post<T = unknown>(
    method: string,
    params: Record<string, unknown> = {}
  ): Promise<T> {
    return this.transport.call<T>(method, params, this.nextId++);
  }

  /**
   * Subscribe to server-sent events for a session.
   *
   * 1. POST runstatus.session.subscribe → {subscription_id}
   * 2. Open EventSource for the stream.
   * 3. On error: close, reconnect with exponential backoff.
   *    After reconnect call getTrace(since_turn) to backfill, then re-open.
   * 4. Returns an unsubscribe function that closes the stream and calls
   *    runstatus.session.unsubscribe.
   */
  subscribe(
    sessionId: string,
    onEvent: (e: import("../types.js").TraceEvent) => void,
    getTrace: (sinceТurn: number) => Promise<{
      events: import("../types.js").TraceEvent[];
      last_turn: number;
    }>,
    onConnectionChange?: (
      state: import("../data/source.js").ConnectionState
    ) => void
  ): () => void {
    let lastTurn = -1;
    let closed = false;
    let unsubStream: (() => void) | null = null;
    let subscriptionId = "";
    const seenEvents = new Set<string>();

    const traceEventKey = (e: import("../types.js").TraceEvent): string =>
      JSON.stringify([
        e.time,
        e.level,
        e.msg,
        e.session_id,
        e.turn,
        e.state_path ?? "",
        e.parent_turn ?? 0,
        e.attrs ?? {},
      ]);

    const deliver = (event: import("../types.js").TraceEvent): void => {
      const key = traceEventKey(event);
      if (seenEvents.has(key)) return;
      seenEvents.add(key);
      if (event.turn > lastTurn) {
        lastTurn = event.turn;
      }
      onEvent(event);
    };

    // Parse a /rpc/events SSE frame and fan a runstatus.event to onEvent. The
    // first frame after a drop also flips the surfaced state back to "connected".
    const onMessage = (raw: string) => {
      onConnectionChange?.("connected");
      try {
        const frame = JSON.parse(raw) as JsonRpcNotification;
        if (frame.method === "runstatus.event" && frame.params !== undefined) {
          const { event } = frame.params as {
            subscription_id: string;
            event: import("../types.js").TraceEvent;
          };
          deliver(event);
        }
      } catch {
        // Malformed frame — ignore.
      }
    };

    // On reconnect, backfill via getTrace(since_turn) before the stream
    // reopens (the transport drives the reopen).
    const onReconnect = async () => {
      const sinceТurn = lastTurn >= 0 ? lastTurn : 0;
      const { events, last_turn } = await getTrace(sinceТurn);
      if (closed) return;
      for (const event of events) {
        deliver(event);
      }
      if (last_turn > lastTurn) {
        lastTurn = last_turn;
      }
    };

    // Kick off: subscribe first, then open the stream via the transport.
    this.post<{ subscription_id: string }>(
      "runstatus.session.subscribe",
      { session_id: sessionId }
    ).then(({ subscription_id }) => {
      if (closed) return;
      unsubStream = this.transport.openEventStream(
        "rpc/events",
        { subscription_id },
        {
          onMessage,
          onReconnect,
          // The stream errored; the transport will back off + reopen. Surface
          // "reconnecting" so the view can show a banner instead of dead air.
          onError: () => onConnectionChange?.("reconnecting"),
        }
      );
      // Surface the subscription id so teardown can unsubscribe it server-side.
      subscriptionId = subscription_id;
    });

    return () => {
      closed = true;
      unsubStream?.();
      unsubStream = null;
      if (subscriptionId) {
        // Fire-and-forget; ignore errors on cleanup.
        this.post("runstatus.session.unsubscribe", {
          subscription_id: subscriptionId,
        }).catch(() => undefined);
      }
    };
  }
}
