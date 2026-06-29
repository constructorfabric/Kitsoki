/**
 * embedView — the host side of the generic, producer-neutral embed protocol.
 *
 * An embedded artifact (a deck, a notebook, any multi-view producer) posts which
 * place it is currently showing to its parent window:
 *
 *   window.parent.postMessage(
 *     { type: 'embed:view', producer, scope, label, count }, '*')
 *
 * `scope` is an OPAQUE token the host round-trips back to target feedback at the
 * thing on screen (e.g. a slide index). The host interprets nothing producer-
 * specific — it just remembers the latest scope so a refine/annotation dispatch
 * can carry it. This is how kitsoki targets "the slide you're looking at" without
 * knowing anything about slidey: slidey (the producer) speaks this protocol.
 *
 * Frame-free + DI-friendly: parseEmbedView is a pure function (unit-tested), and
 * installEmbedViewListener wires it to window with a teardown handle.
 */

export interface EmbedViewMessage {
  /** The producer that owns the embed (e.g. "slidey"). Informational. */
  producer?: string;
  /** The opaque scope token the host round-trips back (e.g. a scene index). */
  scope: string;
  /** Optional producer-native transition/reveal step within the scope. */
  step?: string;
  /** Human label for the current view ("Scene 9 · Cat Wrangling"). */
  label?: string;
  /** Total number of views, when the producer knows it. */
  count?: number;
}

/**
 * parseEmbedView returns the EmbedViewMessage carried by a postMessage event, or
 * null when the event is not a well-formed `embed:view` message. Defensive: a
 * page receives messages from many sources, so anything off-shape is ignored.
 */
export function parseEmbedView(data: unknown): EmbedViewMessage | null {
  if (!data || typeof data !== "object") return null;
  const m = data as Record<string, unknown>;
  if (m.type !== "embed:view") return null;
  // scope is the one required field; coerce a number scope to string so an
  // opaque index round-trips cleanly.
  if (m.scope === undefined || m.scope === null) return null;
  const scope = typeof m.scope === "number" ? String(m.scope) : m.scope;
  if (typeof scope !== "string" || scope === "") return null;
  return {
    producer: typeof m.producer === "string" ? m.producer : undefined,
    scope,
    step:
      typeof m.step === "number"
        ? String(m.step)
        : typeof m.step === "string" && m.step !== ""
          ? m.step
          : undefined,
    label: typeof m.label === "string" ? m.label : undefined,
    count: typeof m.count === "number" ? m.count : undefined,
  };
}

/**
 * installEmbedViewListener subscribes to window 'message' events, parses
 * `embed:view` messages, and calls onView with each. Returns a teardown function
 * that removes the listener. A no-op (returns a no-op teardown) when there is no
 * window (SSR / tests without a DOM).
 */
export function installEmbedViewListener(
  onView: (view: EmbedViewMessage) => void,
  target: Pick<Window, "addEventListener" | "removeEventListener"> | undefined =
    typeof window !== "undefined" ? window : undefined,
): () => void {
  if (!target) return () => {};
  const handler = (ev: Event) => {
    const view = parseEmbedView((ev as MessageEvent).data);
    if (view) onView(view);
  };
  target.addEventListener("message", handler);
  return () => target.removeEventListener("message", handler);
}

// ── Element picking (embed:annotate / embed:pick) ─────────────────────────────
//
// The richer half of the protocol: the host turns on annotation mode on an
// embedded artifact, and the producer — which owns its own live surface — posts
// back a PRECISE anchor when the operator points at an element:
//
//   host → producer:  { type: 'embed:annotate', enabled: boolean }
//   producer → host:  { type: 'embed:pick', producer, scope, ref, label, bbox }
//
// `ref` is the opaque element id (e.g. "9/image") the host round-trips into the
// refine; `scope` is the view it belongs to; `bbox` is an optional on-screen
// rect. The host interprets none of it — it just hands `ref`/`scope` to the
// producer's own resolver/gate.

export interface EmbedPickMessage {
  producer?: string;
  /** The view the picked element belongs to (e.g. a scene index). */
  scope?: string;
  /** The opaque element id the host round-trips into a refine. */
  ref: string;
  /** Human label for the picked element. */
  label?: string;
  /** The element's on-screen rect [x,y,w,h], when the producer supplies it. */
  bbox?: [number, number, number, number];
}

/** parseEmbedPick returns the EmbedPickMessage on a postMessage event, or null. */
export function parseEmbedPick(data: unknown): EmbedPickMessage | null {
  if (!data || typeof data !== "object") return null;
  const m = data as Record<string, unknown>;
  if (m.type !== "embed:pick") return null;
  if (typeof m.ref !== "string" || m.ref === "") return null;
  const scope = typeof m.scope === "number" ? String(m.scope) : m.scope;
  const bbox = Array.isArray(m.bbox) && m.bbox.length === 4 && m.bbox.every((n) => typeof n === "number")
    ? (m.bbox as [number, number, number, number])
    : undefined;
  return {
    producer: typeof m.producer === "string" ? m.producer : undefined,
    scope: typeof scope === "string" ? scope : undefined,
    ref: m.ref,
    label: typeof m.label === "string" ? m.label : undefined,
    bbox,
  };
}

/** installEmbedPickListener subscribes to embed:pick messages. Mirror of the
 *  view listener; returns a teardown. */
export function installEmbedPickListener(
  onPick: (pick: EmbedPickMessage) => void,
  target: Pick<Window, "addEventListener" | "removeEventListener"> | undefined =
    typeof window !== "undefined" ? window : undefined,
): () => void {
  if (!target) return () => {};
  const handler = (ev: Event) => {
    const pick = parseEmbedPick((ev as MessageEvent).data);
    if (pick) onPick(pick);
  };
  target.addEventListener("message", handler);
  return () => target.removeEventListener("message", handler);
}

/** sendAnnotateMode posts the host→producer enable/disable message into an
 *  embedded artifact's window (the iframe's contentWindow). No-op when the
 *  target window is absent (the iframe hasn't loaded yet). */
export function sendAnnotateMode(
  targetWindow: Pick<Window, "postMessage"> | null | undefined,
  enabled: boolean,
  view?: { scope?: string; step?: string },
): void {
  if (!targetWindow) return;
  try {
    targetWindow.postMessage({ type: "embed:annotate", enabled, ...(view ?? {}) }, "*");
  } catch {
    /* cross-origin restriction — ignore */
  }
}
