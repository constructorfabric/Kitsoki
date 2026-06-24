// relay.ts — the host side of the postMessage <-> HTTP/SSE bridge.
//
// The VS Code webview document lives at a `vscode-webview://` origin and cannot
// fetch/EventSource the cross-origin `http://127.0.0.1:PORT` kitsoki backend
// directly. The extension host holds the only HTTP/SSE connection and relays
// over `postMessage` using the envelope protocol defined in the build contract.
//
// This module is deliberately free of any `vscode` import so it can be unit
// tested against a plain stub HTTP server (no real kitsoki, no LLM). The
// transport surface it needs from the host is injected as `RelayPort`.

/** Webview -> host envelopes. `id` is a monotonic int minted in the webview. */
export type InboundEnvelope =
  | { t: 'call'; id: number; method: string; params: Record<string, unknown> }
  | { t: 'evt-open'; id: number; path: string; query: Record<string, string> }
  | { t: 'evt-close'; id: number }
  | { t: 'post-open'; id: number; path: string; body: Record<string, unknown> };

/** Host -> webview envelopes. */
export type OutboundEnvelope =
  | { t: 'call-ok'; id: number; result: unknown }
  | { t: 'call-err'; id: number; error: { code?: number; message: string; data?: unknown } }
  | { t: 'evt-msg'; id: number; data: string }
  | { t: 'evt-err'; id: number }
  | { t: 'post-frame'; id: number; frame: unknown }
  | { t: 'post-done'; id: number; frame: unknown }
  | { t: 'post-err'; id: number; error: { message: string } };

/** The host capabilities the relay needs. Injected for testability. */
export interface RelayPort {
  /** Base URL of the kitsoki backend, e.g. "http://127.0.0.1:54231". No trailing slash required. */
  base: string;
  /** Post an outbound envelope to the webview. */
  post(env: OutboundEnvelope): void;
  /** Optional log sink (OutputChannel.appendLine in production). */
  log?(line: string): void;
}

/** Host owns reconnect; the webview transport does NOT reconnect under the bridge. */
const BACKOFF_MS = [250, 500, 1000, 2000, 5000];

function joinUrl(base: string, path: string): string {
  const b = base.endsWith('/') ? base.slice(0, -1) : base;
  const p = path.startsWith('/') ? path.slice(1) : path;
  return `${b}/${p}`;
}

/**
 * Reads an SSE body (text/event-stream) line by line, invoking `onFrame` with
 * each parsed JSON `data:` payload. Resolves when the stream ends. `signal`
 * aborts the read.
 */
async function readSse(
  body: ReadableStream<Uint8Array>,
  onData: (payload: string) => void,
  signal?: AbortSignal,
): Promise<void> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buf = '';
  try {
    for (;;) {
      if (signal?.aborted) return;
      const { value, done } = await reader.read();
      if (done) break;
      buf += decoder.decode(value, { stream: true });
      let nl: number;
      // SSE frames are separated by blank lines; but kitsoki emits one JSON
      // object per `data:` line, so we process complete lines as they arrive.
      while ((nl = buf.indexOf('\n')) >= 0) {
        const line = buf.slice(0, nl).replace(/\r$/, '');
        buf = buf.slice(nl + 1);
        if (!line.startsWith('data:')) continue;
        const payload = line.slice(5).trim();
        if (!payload) continue;
        // Forward the RAW data payload string. The webview's data layer
        // (JsonRpcClient.subscribe / LiveSource.subscribe*) JSON.parses it —
        // exactly as HttpTransport passes EventSource `ev.data` through. The
        // post-SSE path parses here to detect the done/error sentinel.
        onData(payload);
      }
    }
  } finally {
    reader.releaseLock();
  }
}

/**
 * Relay owns one webview's connection to the backend. Construct one per
 * resolved webview; call `handle()` for each inbound envelope and `dispose()`
 * when the webview is torn down.
 */
export class Relay {
  private readonly port: RelayPort;
  /** Open GET-SSE channels keyed by webview-minted id -> abort controller. */
  private readonly eventStreams = new Map<number, AbortController>();
  /** Open POST-SSE channels keyed by id -> abort controller. */
  private readonly postStreams = new Map<number, AbortController>();
  private disposed = false;

  constructor(port: RelayPort) {
    this.port = port;
  }

  /** Update the backend base URL (e.g. once the backend has become healthy). */
  setBase(base: string): void {
    this.port.base = base;
  }

  /**
   * Abort every in-flight GET/POST stream and forget them, leaving the relay
   * USABLE for new envelopes. Use this when the backend has restarted on a new
   * port: each stream's reconnect loop captured its URL at open time against the
   * OLD base, so a bare setBase() would leave them hammering the dead port
   * forever. The webview reboots after this and re-opens its streams against the
   * new base. Distinct from dispose(), which retires the relay for good.
   */
  resetStreams(): void {
    for (const ctrl of this.eventStreams.values()) ctrl.abort();
    for (const ctrl of this.postStreams.values()) ctrl.abort();
    this.eventStreams.clear();
    this.postStreams.clear();
  }

  handle(env: InboundEnvelope): void {
    if (this.disposed) return;
    switch (env.t) {
      case 'call':
        void this.handleCall(env);
        return;
      case 'evt-open':
        this.handleEvtOpen(env);
        return;
      case 'evt-close':
        this.handleEvtClose(env);
        return;
      case 'post-open':
        void this.handlePostOpen(env);
        return;
    }
  }

  private log(line: string): void {
    this.port.log?.(line);
  }

  /** call -> POST /rpc, unwrap JSON-RPC result/error. */
  private async handleCall(env: Extract<InboundEnvelope, { t: 'call' }>): Promise<void> {
    const url = joinUrl(this.port.base, 'rpc');
    try {
      const res = await fetch(url, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ jsonrpc: '2.0', id: env.id, method: env.method, params: env.params }),
      });
      if (!res.ok) {
        this.port.post({ t: 'call-err', id: env.id, error: { message: `HTTP ${res.status}` } });
        return;
      }
      const json = (await res.json()) as {
        result?: unknown;
        error?: { code?: number; message: string; data?: unknown };
      };
      if (json.error) {
        this.port.post({ t: 'call-err', id: env.id, error: json.error });
        return;
      }
      this.port.post({ t: 'call-ok', id: env.id, result: json.result });
    } catch (e) {
      this.port.post({ t: 'call-err', id: env.id, error: { message: (e as Error).message } });
    }
  }

  /** evt-open -> GET SSE with host-owned backoff reconnect. */
  private handleEvtOpen(env: Extract<InboundEnvelope, { t: 'evt-open' }>): void {
    const ctrl = new AbortController();
    this.eventStreams.set(env.id, ctrl);
    void this.runEventStream(env, ctrl);
  }

  private async runEventStream(
    env: Extract<InboundEnvelope, { t: 'evt-open' }>,
    ctrl: AbortController,
  ): Promise<void> {
    const qs = new URLSearchParams(env.query).toString();
    const url = joinUrl(this.port.base, env.path) + (qs ? `?${qs}` : '');
    let attempt = 0;
    while (!ctrl.signal.aborted && !this.disposed) {
      try {
        const res = await fetch(url, {
          headers: { accept: 'text/event-stream' },
          signal: ctrl.signal,
        });
        if (!res.ok || !res.body) throw new Error(`HTTP ${res.status}`);
        attempt = 0; // a successful connect resets backoff
        await readSse(
          res.body,
          (payload) => this.port.post({ t: 'evt-msg', id: env.id, data: payload }),
          ctrl.signal,
        );
        // Stream ended cleanly; reconnect (the channel is long-lived).
      } catch (e) {
        if (ctrl.signal.aborted) return;
        this.port.post({ t: 'evt-err', id: env.id });
        this.log(`evt-stream ${env.path} error: ${(e as Error).message}`);
      }
      if (ctrl.signal.aborted || this.disposed) return;
      const delay = BACKOFF_MS[Math.min(attempt, BACKOFF_MS.length - 1)];
      attempt++;
      await sleep(delay, ctrl.signal);
    }
  }

  private handleEvtClose(env: Extract<InboundEnvelope, { t: 'evt-close' }>): void {
    const ctrl = this.eventStreams.get(env.id);
    if (ctrl) {
      ctrl.abort();
      this.eventStreams.delete(env.id);
    }
  }

  /**
   * post-open -> POST SSE. turn-stream/meta-stream use `{type:"done",result}` /
   * `{type:"error",message}` sentinels; forward intermediate frames as
   * post-frame, terminate on the sentinel with post-done / post-err.
   */
  private async handlePostOpen(env: Extract<InboundEnvelope, { t: 'post-open' }>): Promise<void> {
    const ctrl = new AbortController();
    this.postStreams.set(env.id, ctrl);
    const url = joinUrl(this.port.base, env.path);
    let settled = false;
    const settle = (out: OutboundEnvelope) => {
      if (settled) return;
      settled = true;
      this.postStreams.delete(env.id);
      this.port.post(out);
    };
    try {
      const res = await fetch(url, {
        method: 'POST',
        headers: { 'content-type': 'application/json', accept: 'text/event-stream' },
        body: JSON.stringify(env.body),
        signal: ctrl.signal,
      });
      if (!res.ok || !res.body) throw new Error(`HTTP ${res.status}`);
      await readSse(
        res.body,
        (payload) => {
          if (settled) return;
          let frame: unknown;
          try {
            frame = JSON.parse(payload);
          } catch {
            return; // ignore non-JSON keepalive/comment lines
          }
          const f = frame as { type?: string; message?: string };
          if (f && f.type === 'done') {
            settle({ t: 'post-done', id: env.id, frame });
          } else if (f && f.type === 'error') {
            settle({ t: 'post-err', id: env.id, error: { message: f.message ?? 'stream error' } });
          } else {
            this.port.post({ t: 'post-frame', id: env.id, frame });
          }
        },
        ctrl.signal,
      );
      // Stream ended without an explicit sentinel — treat as error so the
      // webview's pending promise is never left hanging.
      settle({ t: 'post-err', id: env.id, error: { message: 'stream closed without done sentinel' } });
    } catch (e) {
      if (ctrl.signal.aborted) {
        settle({ t: 'post-err', id: env.id, error: { message: 'aborted' } });
        return;
      }
      settle({ t: 'post-err', id: env.id, error: { message: (e as Error).message } });
    }
  }

  dispose(): void {
    this.disposed = true;
    this.resetStreams();
  }
}

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    if (signal?.aborted) return resolve();
    const t = setTimeout(resolve, ms);
    signal?.addEventListener('abort', () => {
      clearTimeout(t);
      resolve();
    }, { once: true });
  });
}
