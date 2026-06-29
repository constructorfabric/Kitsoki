/**
 * Session capture: an injectable singleton that records the DOM session with
 * rrweb into a rolling buffer keeping roughly the last ~30s of activity.
 *
 * rrweb emits a "full snapshot" on each checkout (we ask for one every 15s).
 * To bound memory we keep at most the two most recent checkpoints: when a new
 * full snapshot arrives we drop every event recorded before the *previous*
 * checkpoint, so a snapshot always starts from a self-contained full snapshot
 * and spans ~30s (2 × 15s).
 *
 * rrweb is injectable (the record fn) so tests can drive the buffer with a stub
 * emitter and never load the real, DOM-heavy library.
 *
 * Privacy: the buffer is written into a committed bug artifact, so masking is
 * the safety boundary. We record with maskAllInputs + maskAllText (every form
 * value and text node masked) and block password inputs; the server then runs
 * credential-pattern scrubbing over the serialized events as a second layer.
 * What survives is interaction flow and layout — the replay's real value.
 */

/** An rrweb event. Opaque to us; type=2 is a FullSnapshot (a checkout point). */
export interface RrwebEvent {
  type: number;
  data?: unknown;
  timestamp?: number;
  [k: string]: unknown;
}

export interface RrwebEnvelope {
  schemaVersion: 1;
  source: string;
  viewport: { width: number; height: number; deviceScaleFactor?: number };
  startTime: number;
  endTime: number;
  durationMs: number;
  events: RrwebEvent[];
}

const FULL_SNAPSHOT = 2;
// type=4 is a Meta event (href + viewport width/height). The Replayer needs a
// Meta before the first FullSnapshot to size its iframe; without it the replay
// renders a blank frame. rrweb emits exactly one Meta at record start, BUT it
// does NOT re-emit it on later checkouts — so once buffer trimming drops the
// original Meta we must re-prepend it, or every capture past the first checkout
// (≈15s in) replays blank.
const META = 4;

export interface RecordOptions {
  emit: (event: RrwebEvent) => void;
  checkoutEveryNms?: number;
  maskAllInputs?: boolean;
  // Mask ALL text nodes in the recorded DOM (rendered as a fixed-width mask in
  // replay). maskAllInputs only covers form *values* — it does NOT touch text
  // nodes, so story prose, prompts, trace text, operator Q&A etc. would
  // otherwise be serialized verbatim into the committed rrweb.json. Because the
  // artifacts are committed to the repo, masking is the privacy boundary, so we
  // default this on; interaction flow and layout (the replay's actual debugging
  // value) survive masking.
  maskAllText?: boolean;
  blockSelector?: string;
}

/** Minimal shape of rrweb's record() function. */
export type RrwebRecord = (opts: RecordOptions) => (() => void) | undefined;

let buffer: RrwebEvent[] = [];
// Index (into buffer) of the previous full-snapshot checkpoint. -1 until the
// first checkout.
let prevCheckpointIdx = -1;
// The first Meta event (type=4) rrweb emits at record start. Retained verbatim
// so we can re-prepend it to a snapshot whose buffer trimming has dropped it —
// otherwise the Replayer can't size its iframe and renders blank.
let firstMeta: RrwebEvent | null = null;
let stopFn: (() => void) | undefined;
let started = false;

function onEmit(event: RrwebEvent): void {
  try {
    if (event.type === META && !firstMeta) {
      firstMeta = event;
    }
    if (event.type === FULL_SNAPSHOT) {
      // A new checkpoint. Drop everything before the *previous* checkpoint so
      // we retain ~2 checkpoints (≈30s). prevCheckpointIdx tracks where the
      // last checkpoint started.
      if (prevCheckpointIdx > 0) {
        buffer = buffer.slice(prevCheckpointIdx);
        prevCheckpointIdx = -1;
        // Re-find the (now relocated) previous checkpoint at index 0.
      }
      prevCheckpointIdx = buffer.length;
    }
    buffer.push(event);
  } catch {
    /* never throw */
  }
}

/**
 * Start rrweb recording into the rolling buffer. Pass an rrweb record() impl
 * (defaults to the real one, lazily loaded). Idempotent: a second call is a
 * no-op while a capture is active.
 */
export function startSessionCapture(rrwebRecord?: RrwebRecord): void {
  if (started) return;
  started = true;
  const opts: RecordOptions = {
    emit: onEmit,
    checkoutEveryNms: 15000,
    maskAllInputs: true,
    // Privacy boundary: the buffer is committed to the repo (see RecordOptions).
    // Mask form values AND all text nodes; the server additionally runs
    // credential-pattern scrubbing over the serialized events as a second layer.
    maskAllText: true,
    blockSelector: 'input[type="password"]',
  };
  try {
    if (rrwebRecord) {
      stopFn = rrwebRecord(opts) ?? undefined;
    } else {
      // Lazy-load the real rrweb so it isn't eager in the main bundle.
      void import("rrweb")
        .then((mod) => {
          const rec = (mod as { record?: RrwebRecord }).record;
          if (rec) stopFn = rec(opts) ?? undefined;
        })
        .catch(() => {
          /* recording unavailable — non-fatal */
        });
    }
  } catch {
    /* never let capture init throw */
  }
}

/**
 * Snapshot the current rolling buffer (a copy). If buffer trimming has dropped
 * the original Meta event (so the buffer now starts at a FullSnapshot with no
 * preceding Meta), re-prepend the retained first Meta — the Replayer needs a
 * Meta before the first FullSnapshot or it renders a blank frame.
 */
export function snapshotSessionEvents(): RrwebEvent[] {
  const events = buffer.slice();
  const firstFullIdx = events.findIndex((e) => e.type === FULL_SNAPSHOT);
  if (firstFullIdx >= 0) {
    const metaBeforeFull = events
      .slice(0, firstFullIdx)
      .some((e) => e.type === META);
    if (!metaBeforeFull && firstMeta) {
      events.unshift(firstMeta);
    }
  }
  return events;
}

export function buildSessionEnvelope(
  events: RrwebEvent[] = snapshotSessionEvents(),
  opts: { source?: string; viewport?: { width: number; height: number; deviceScaleFactor?: number } } = {}
): RrwebEnvelope {
  const first = timestampOf(events[0], 0);
  const last = timestampOf(events[events.length - 1], first);
  return {
    schemaVersion: 1,
    source: opts.source ?? "kitsoki-visual-record",
    viewport: opts.viewport ?? rrwebViewport(events),
    startTime: first,
    endTime: last,
    durationMs: Math.max(0, last - first),
    events,
  };
}

function timestampOf(event: RrwebEvent | undefined, fallback: number): number {
  return typeof event?.timestamp === "number" ? event.timestamp : fallback;
}

function rrwebViewport(events: RrwebEvent[]): { width: number; height: number } {
  const meta = events.find((event) => event.type === META && event.data && typeof event.data === "object");
  const data = meta?.data as { width?: unknown; height?: unknown } | undefined;
  if (typeof data?.width === "number" && typeof data?.height === "number") {
    return { width: data.width, height: data.height };
  }
  return { width: window.innerWidth || 1600, height: window.innerHeight || 900 };
}

/** Stop recording and reset. Mainly for tests. */
export function __resetSessionCapture(): void {
  try {
    stopFn?.();
  } catch {
    /* ignore */
  }
  stopFn = undefined;
  buffer = [];
  prevCheckpointIdx = -1;
  firstMeta = null;
  started = false;
}
