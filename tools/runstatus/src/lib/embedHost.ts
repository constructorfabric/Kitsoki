/**
 * embedHost — the embed side of the host<->iframe postMessage contract
 * (portal-kitsoki-chat-embed-plan.md §3.2). Deliberately tiny and
 * versioned (`v: 0`) so extraction into @kitsoki/ui-sdk later is
 * mechanical; this module has no Vue/kitsoki coupling beyond `window`.
 *
 * embed -> host: {kitsoki:"ready", v:0} on mount;
 *   {kitsoki:"event", v:0, type, session_id, ...} as the session progresses.
 * host -> embed: {kitsoki:"context", v:0, catalog, view, node_ids, filters,
 *   instruction?} — the caller folds this into world.portal_context.
 *
 * targetOrigin: an explicit `?origin=` boot param wins (embedBoot.ts);
 * otherwise derived from document.referrer's origin. Falls back to "*"
 * only when neither is available (e.g. direct browser navigation during
 * dev) so postMessage still no-ops safely with no parent listening.
 */

export interface EmbedEvent {
  kitsoki: "event";
  v: 0;
  type: "session_started" | "turn_done" | "error";
  session_id?: string;
  message?: string;
}

export interface EmbedContext {
  kitsoki: "context";
  v: 0;
  catalog?: string;
  view?: string;
  node_ids?: string[];
  filters?: Record<string, unknown>;
  instruction?: string;
}

function isEmbedContext(data: unknown): data is EmbedContext {
  if (!data || typeof data !== "object") return false;
  const m = data as Record<string, unknown>;
  return m.kitsoki === "context" && m.v === 0;
}

function referrerOrigin(): string | null {
  try {
    return document.referrer ? new URL(document.referrer).origin : null;
  } catch {
    return null;
  }
}

export class EmbedHost {
  private readonly targetOrigin: string;

  constructor(originParam: string | null) {
    this.targetOrigin = originParam || referrerOrigin() || "*";
  }

  private post(msg: unknown): void {
    if (!window.parent || window.parent === window) return;
    window.parent.postMessage(msg, this.targetOrigin);
  }

  sendReady(): void {
    this.post({ kitsoki: "ready", v: 0 });
  }

  sendEvent(type: EmbedEvent["type"], extra: Partial<EmbedEvent> = {}): void {
    this.post({ kitsoki: "event", v: 0, type, ...extra });
  }

  /**
   * Resolve with the first `context` message received, or null once
   * `timeoutMs` elapses with none. Used once at boot so autostart's
   * session.new can seed world.portal_context from the first payload
   * instead of racing it.
   */
  waitForContext(timeoutMs = 300): Promise<EmbedContext | null> {
    return new Promise((resolve) => {
      let settled = false;
      const onMessage = (ev: MessageEvent) => {
        if (!isEmbedContext(ev.data)) return;
        if (settled) return;
        settled = true;
        window.removeEventListener("message", onMessage);
        clearTimeout(timer);
        resolve(ev.data);
      };
      const timer = setTimeout(() => {
        if (settled) return;
        settled = true;
        window.removeEventListener("message", onMessage);
        resolve(null);
      }, timeoutMs);
      window.addEventListener("message", onMessage);
    });
  }
}
