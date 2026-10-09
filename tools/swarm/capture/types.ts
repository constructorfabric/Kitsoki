/**
 * types.ts — the shape of one swarm finding-capture bundle.
 *
 * A "finding" is anything a swarm run's per-user gates (isolation, console,
 * audit) — or a driver's explicit `recordFinding` call — decides is worth
 * preserving evidence for. Today reproducing a swarm failure means re-running
 * the whole N-user swarm; a capture bundle makes each failure self-contained
 * and independently replayable, using the SAME evidence machinery the
 * interactive "Report a bug" flow already produces (rrweb + console + a
 * server-scrubbed HAR) rather than inventing a parallel capture format.
 */

/** One rrweb event, opaque to us (mirrors session-capture.ts's RrwebEvent). */
export interface CaptureRrwebEvent {
  type: number;
  data?: unknown;
  timestamp?: number;
  [k: string]: unknown;
}

/** The rrweb envelope shape returned by `window.__kitsokiVisual.recording()`
 *  (tools/runstatus/src/data/session-capture.ts's RrwebEnvelope). Duplicated
 *  here (not imported) because tools/swarm has no access to tools/runstatus's
 *  node_modules-rooted TS project at runtime — see tools/swarm/README.md's
 *  "why no dependencies" note. This is a structural, not nominal, match. */
export interface CaptureRrwebEnvelope {
  schemaVersion: 1;
  source: string;
  viewport: { width: number; height: number; deviceScaleFactor?: number };
  startTime: number;
  endTime: number;
  durationMs: number;
  events: CaptureRrwebEvent[];
}

/** Minimal RPC function shape a capture needs — the same signature
 *  `server.rpc<T>` / `wrapRpcWithRetry`'s wrapped function already has. */
export type CaptureRpcFn = <T>(method: string, params: Record<string, unknown>) => Promise<T>;

/** What identifies the finding being captured: who hit it, where, and why. */
export interface FindingContext {
  /** Persona id driving the failing user (e.g. from personas.ts). */
  persona_id: string;
  /** The swarm's per-user index, for cross-referencing results.json. */
  user_index?: number;
  /** The isolation marker for this user, if one was minted (isolation.ts). */
  marker?: string;
  /** The journey/FSM step (state) the user was in or driving toward. */
  journey_step: string;
  /** Human-readable description of the failed assertion/gate. */
  assertion: string;
  /** Free-form extra context (gate name, counts, sample errors, ...). */
  detail?: Record<string, unknown>;
}

/** The on-disk manifest written as manifest.json inside every bundle
 *  directory. Self-describing: a reader needs nothing else to know what
 *  failed, for whom, and against which server build. */
export interface FindingManifest {
  capture_id: string;
  captured_at: string;
  persona_id: string;
  user_index: number | null;
  marker: string | null;
  journey_step: string;
  assertion: string;
  detail: Record<string, unknown>;
  server_sha: string;
  /** Relative (to the bundle dir) file names actually written. Any of these
   *  may be absent if that leg of evidence was unavailable (best-effort). */
  files: {
    rrweb: string | null;
    console: string | null;
    har: string | null;
  };
  /** Counts, so a reader can sanity-check completeness without opening every
   *  file. */
  counts: {
    rrweb_events: number;
    console_entries: number;
    har_depth: number | null;
  };
}

/** Result of a recordFinding() call: the manifest plus the absolute bundle
 *  directory it was written under. */
export interface FindingBundle {
  manifest: FindingManifest;
  dir: string;
}
