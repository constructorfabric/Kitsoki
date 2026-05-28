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

// Backoff schedule: 250, 500, 1000, 2000, then cap at 5000 ms.
const BACKOFF_MS = [250, 500, 1000, 2000, 5000];

function nextBackoff(attempt: number): number {
  return BACKOFF_MS[Math.min(attempt, BACKOFF_MS.length - 1)] ?? 5000;
}

export class JsonRpcClient {
  private readonly base: string;
  private nextId = 1;

  constructor(base = "/") {
    // Normalise: ensure it ends with "/"
    this.base = base.endsWith("/") ? base : base + "/";
  }

  async post<T = unknown>(
    method: string,
    params: Record<string, unknown> = {}
  ): Promise<T> {
    const id = this.nextId++;
    const body: JsonRpcRequest = { jsonrpc: "2.0", id, method, params };

    const resp = await fetch(`${this.base}rpc`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });

    if (!resp.ok) {
      throw new JsonRpcError(
        resp.status,
        `HTTP ${resp.status}: ${resp.statusText}`
      );
    }

    const frame = (await resp.json()) as JsonRpcResponse<T>;

    if (frame.error !== undefined) {
      throw new JsonRpcError(
        frame.error.code,
        frame.error.message,
        frame.error.data
      );
    }

    return frame.result as T;
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
    }>
  ): () => void {
    let subscriptionId = "";
    let es: EventSource | null = null;
    let lastTurn = -1;
    let closed = false;
    let backoffAttempt = 0;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

    const openStream = () => {
      if (closed) return;

      const url = `${this.base}rpc/events?subscription_id=${encodeURIComponent(subscriptionId)}`;
      es = new EventSource(url);

      es.onmessage = (ev: MessageEvent<string>) => {
        backoffAttempt = 0; // reset on successful message
        try {
          const frame = JSON.parse(ev.data) as JsonRpcNotification;
          if (
            frame.method === "runstatus.event" &&
            frame.params !== undefined
          ) {
            const { event } = frame.params as {
              subscription_id: string;
              event: import("../types.js").TraceEvent;
            };
            if (event.turn > lastTurn) {
              lastTurn = event.turn;
            }
            onEvent(event);
          }
        } catch {
          // Malformed frame — ignore.
        }
      };

      es.onerror = () => {
        if (closed) return;
        es?.close();
        es = null;

        const delay = nextBackoff(backoffAttempt++);
        reconnectTimer = setTimeout(() => {
          if (closed) return;
          const sinceТurn = lastTurn >= 0 ? lastTurn + 1 : 0;
          getTrace(sinceТurn)
            .then(({ events, last_turn }) => {
              if (closed) return;
              for (const event of events) {
                if (event.turn > lastTurn) {
                  lastTurn = event.turn;
                  onEvent(event);
                }
              }
              if (last_turn > lastTurn) {
                lastTurn = last_turn;
              }
              openStream();
            })
            .catch(() => {
              if (!closed) openStream();
            });
        }, delay);
      };
    };

    // Kick off: subscribe first, then open stream.
    this.post<{ subscription_id: string }>(
      "runstatus.session.subscribe",
      { session_id: sessionId }
    ).then(({ subscription_id }) => {
      if (closed) return;
      subscriptionId = subscription_id;
      openStream();
    });

    return () => {
      closed = true;
      if (reconnectTimer !== null) {
        clearTimeout(reconnectTimer);
      }
      es?.close();
      es = null;
      if (subscriptionId) {
        // Fire-and-forget; ignore errors on cleanup.
        this.post("runstatus.session.unsubscribe", {
          subscription_id: subscriptionId,
        }).catch(() => undefined);
      }
    };
  }
}
