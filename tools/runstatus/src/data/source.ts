import type {
  SessionHeader,
  AppDef,
  MermaidSnapshot,
  TraceEvent,
  TurnResult,
  HarnessState,
} from "../types.js";
import type { TranscriptData } from "./transcript.js";
import type { StreamItem } from "../lib/activity.js";
import type { ResolvedElement } from "../lib/resolveElement.js";
import type { AnnotationAnchor } from "../lib/annotationAnchor.js";
import type { SemanticSidecar } from "../lib/semanticPlugins.js";
import { SnapshotSource } from "./snapshot-source.js";
import { LiveSource } from "./live-source.js";

/**
 * VisualBundle — the spatial-capture ambient attached to an off-path question
 * (docs/tui/spatial-capture.md). It rides on
 * `runstatus.session.offpath`'s optional `visual` param, which slice 1 lifts
 * server-side into host.WithVisualAmbient so the converse oracle answers with
 * the frame, the pixel, and the element in context. Every field is optional:
 * the bundle is forward-compatible (a future static-image upload carries only
 * `frame_handle` + `point`, no `element`).
 */
export interface VisualBundle {
  /** Artifact handle of the captured still the operator pointed at. */
  frame_handle?: string;
  /** The originating media artifact handle (the video/image the frame came from). */
  media_handle?: string;
  /** The click position within the frame, in frame pixels. */
  point?: { x: number; y: number };
  /** The DOM element resolved under the point (lib/resolveElement). */
  element?: ResolvedElement;
  /** Frame timestamp within the source video, if any. */
  t_ms?: number;
  /** The route the capture happened on (e.g. "/review/<sid>"). */
  route?: string;
}

export interface TraceCursor {
  since_turn?: number;
  until_turn?: number;
  limit?: number;
}

/**
 * Liveness of a session's SSE trace stream, surfaced so the UI can show a
 * visible "Reconnecting to session…" banner instead of dead air when the
 * stream drops (the transport reconnects with backoff invisibly otherwise).
 *
 *   - "connected"    — the stream is open and delivering frames.
 *   - "reconnecting" — the stream errored; the transport is backing off and
 *                      will reopen (and backfill) shortly.
 */
export type ConnectionState = "connected" | "reconnecting";

// ── Meta mode (overlay chat) wire types ────────────────────────────────────
// Mirror internal/runstatus/server/meta.go.

/** One selectable mode in the meta dropdown. */
export interface MetaModeInfo {
  key: string; // "story.edit" | "story.ask" | "story.improve" | "kitsoki.ask" | …
  label: string;
  banner: string;
  agent: string;
  read_only: boolean;
  group: string; // "story" | "kitsoki"
}

/** One transcript turn. role: "user" | "assistant". */
export interface MetaMessage {
  role: string;
  text: string;
  /**
   * The turn's thinking/tool activity feed in arrival order (assistant
   * messages only). Client-enriched during a streamed turn — the server's
   * persisted transcript carries role+text only, so rehydrated messages
   * arrive without it.
   */
  stream?: StreamItem[];
}

/** Handle returned by enter / new: the chat row + its transcript so far. */
export interface MetaSession {
  chat_id: string;
  mode_key: string;
  messages: MetaMessage[];
}

/** Outcome of one meta turn. The reload_* fields drive in-place content refresh. */
export interface MetaSendResult {
  assistant: string;
  chat_id: string;
  reload_requested: boolean;
  changed_files: string[];
  commit_sha?: string;
}

// ── Video feedback mode (/review) wire types ───────────────────────────────
// Mirror internal/video.Chapter / SourceRef and the server's FeedbackNote.

/** Names the producing unit a chapter came from (slidey scene / tour step). */
export interface SourceRef {
  kind: string; // "slidey" | "tour"
  spec_path: string;
  scene_id?: string;
  step_id?: string;
  line?: number;
}

/** One [start_ms, end_ms) window of a video mapped to its SourceRef. */
export interface Chapter {
  index: number;
  id: string;
  label: string;
  start_ms: number;
  end_ms: number;
  source_ref: SourceRef;
}

/** The structured note runstatus.feedback.add persists + dispatches. */
export interface FeedbackNote {
  video: string; // video artifact handle
  source_ref?: SourceRef;
  time_range?: { start_ms: number; end_ms?: number };
  frame_handle?: string;
  instruction: string;
  /**
   * The unified annotation anchor (v2: png/mp4/rrweb/html/slidey). When present
   * it supersedes the flat time_range/frame_handle fields — those stay populated
   * for back-compat with a server that has not yet read `anchor`. The backend
   * slice lifts it into the agent ambient (lib/annotationAnchor).
   */
  anchor?: AnnotationAnchor;
}

export interface WorkflowValidationReport {
  ok: boolean;
  errors: string[];
  warnings: string[];
}

export interface WorkflowReceipt {
  workflow_id: string;
  goal: string;
  slug: string;
  created_at: string;
  draft_dir: string;
  template_story_dir: string;
  app_path: string;
  manifest_path: string;
  launch_basis_path: string;
  trace_path?: string;
  events_path?: string;
  launch_command?: string;
  validation: WorkflowValidationReport;
  validation_path?: string;
  export_path?: string;
  export_report_path?: string;
  session_id?: string;
  session_handle?: string;
  url?: string;
}

export interface WorkflowExportOptions {
  target?: string;
  allow_base_story?: boolean;
}

export interface DataSource {
  listSessions(): Promise<SessionHeader[]>;
  getSession(sessionId: string): Promise<SessionHeader>;
  getApp(sessionId: string): Promise<AppDef>;
  getMermaid(sessionId: string, detail?: string): Promise<MermaidSnapshot>;
  getTrace(
    sessionId: string,
    cursor?: TraceCursor
  ): Promise<{ events: TraceEvent[]; last_turn: number }>;
  /**
   * Subscribe to a session's live trace stream. `onConnectionChange`, when
   * supplied, is invoked as the stream's liveness changes ("reconnecting" on a
   * drop, "connected" once frames flow again) so the UI can surface a banner.
   * Returns an unsubscribe function.
   */
  subscribe(
    sessionId: string,
    onEvent: (e: TraceEvent) => void,
    onConnectionChange?: (state: ConnectionState) => void
  ): () => void;

  // ── Active-session discovery ─────────────────────────────────────────────
  // Trace-only and graph-only surfaces have no chat to start a session, so they
  // discover and follow the single active (current) session.

  /**
   * Read the current (most recently created/attached) session id, or null when
   * there is no current session. LiveSource hits runstatus.session.current;
   * SnapshotSource returns the snapshot's session id.
   */
  getCurrentSession(): Promise<string | null>;
  /**
   * Subscribe to changes of the current session. onChange is invoked with the
   * new session id (or null) whenever it changes; LiveSource also seeds the
   * latest value on subscribe so a late subscriber syncs. Returns an unsubscribe
   * disposer.
   */
  subscribeCurrentSession(
    onChange: (sessionId: string | null) => void
  ): () => void;

  // ── Write/read RPCs (live session only) ──────────────────────────────────

  /** Read the current room's typed view + allowed intents without advancing. */
  view(sessionId: string): Promise<TurnResult>;
  /** Submit an explicit intent (+ slots) and advance the session. */
  submit(
    sessionId: string,
    intent: string,
    slots?: Record<string, unknown>,
    anchor?: import("../lib/annotationAnchor.js").AnnotationAnchor
  ): Promise<TurnResult>;
  /** Free-text turn: hand raw input to the interpreter to pick an intent. */
  sendTurn(sessionId: string, input: string): Promise<TurnResult>;
  /** Supply missing slots to a clarifying turn and continue. */
  continueTurn(
    sessionId: string,
    slots: Record<string, unknown>
  ): Promise<TurnResult>;
  /** Drive a running autonomous/supervised operation until it stops at a safe checkpoint. */
  driveOperation(sessionId: string): Promise<TurnResult>;
  /**
   * Read-only off-path question against the default agent. An optional
   * `visual` bundle (spatial-capture) attaches the frame + point + resolved
   * element so the agent answers in screen context; slice 1 lifts it into
   * host.WithVisualAmbient server-side.
   *
   * `anchor` is the v2 unified annotation attachment (png/mp4/rrweb/html/slidey
   * — lib/annotationAnchor). When supplied it rides alongside `visual` (the
   * back-compat projection) so a server that reads either gets context; the
   * backend slice lifts the richer `anchor` into the agent ambient.
   */
  offpath(
    sessionId: string,
    input: string,
    visual?: VisualBundle,
    anchor?: AnnotationAnchor
  ): Promise<{ answer: string }>;

  /**
   * Rewind one contextual-routing (CRR) decision: reverse the route identified
   * by decisionId and re-dispatch the original utterance, optionally under a new
   * class. Returns the re-dispatched turn. Live session only; sources without an
   * orchestrator omit it. The engine reverses the lane classes today; an intent-
   * class rewind rejects with a "not yet implemented" error until the journal
   * can recover the accepted intent again.
   */
  rewindRoute?(
    sessionId: string,
    decisionId: string,
    newClass?: string,
    reason?: string,
    workspacePath?: string
  ): Promise<TurnResult>;

  /**
   * Record an operator up/down verdict on a routed turn (the web chat's
   * thumbs-up/down control, WS-C C4) — journals through the same event the
   * TUI's `/route up|down` command writes
   * (Orchestrator.RecordRoutingFeedback). Does not advance the turn: no
   * TurnResult, no server-side state change. Live session only; sources
   * without an orchestrator omit it (the control stays hidden).
   */
  routingFeedback?(
    sessionId: string,
    feedback: {
      state: string;
      intent: string;
      phrase: string;
      tier: string;
      verdict: "up" | "down";
    }
  ): Promise<void>;

  // ── Harness profiles (optional; live session only) ───────────────────────
  // Sources without an orchestrator (artifact/snapshot) omit these, so the
  // header picker stays hidden. Mirrors the server's optional HarnessController.

  /** Read the declared harness profiles + live selection (no secrets). */
  getHarness?(sessionId: string): Promise<HarnessState>;
  /** Switch the active profile (+ optional model / effort), effective next turn. */
  setSelection?(
    sessionId: string,
    profile: string,
    model?: string,
    effort?: string
  ): Promise<HarnessState>;

  // ── Meta mode (overlay chat) ─────────────────────────────────────────────
  // sessionId "" targets the home-screen session-less "self" driver (kitsoki.*
  // modes); a non-empty id targets that session's per-state driver.

  /** List the meta modes available in this scope (for the dropdown). */
  metaModes(sessionId: string): Promise<MetaModeInfo[]>;
  /** Resolve/resume a mode's chat; returns the transcript so far. */
  metaEnter(
    sessionId: string,
    mode: string,
    chatId?: string
  ): Promise<MetaSession>;
  /** Issue one meta turn. */
  metaSend(
    sessionId: string,
    mode: string,
    chatId: string,
    input: string
  ): Promise<MetaSendResult>;
  /** Archive the mode's chat and open a fresh one. */
  metaNew(
    sessionId: string,
    mode: string,
    chatId: string
  ): Promise<MetaSession>;
  /** Read a chat row's transcript (for rehydration). */
  metaTranscript(sessionId: string, chatId: string): Promise<MetaMessage[]>;

  // ── Agent-action transcripts ──────────────────────────────────────────────

  /**
   * Fetch one agent call's agent-action transcript (the verbatim
   * backend-native event stream + capture-time offsets) keyed by its
   * deterministic call_id. LiveSource hits runstatus.session.transcript (lazy
   * server-side sidecar read); SnapshotSource resolves the inlined
   * attrs.transcript that artifact.go folded into the static export. A call with
   * no transcript_ref resolves to an empty TranscriptData (no drawer body).
   */
  getTranscript(sessionId: string, callId: string): Promise<TranscriptData>;

  // ── Media artifacts ───────────────────────────────────────────────────────

  /**
   * Resolve a URL for a named artifact handle. In live mode returns the
   * server-side `/artifact/<handle>` path; in snapshot mode returns a
   * relative sidecar path `./artifacts/<handle>` (the handle is the
   * filename under the snapshot's sibling `artifacts/` directory).
   *
   * maxDim, when set, requests a downscaled still no larger than maxDim pixels
   * on its longest edge — a hint for a heavy frame rendered as a message
   * thumbnail (docs/tracing/trace-format.md, full-res on
   * click-to-zoom). Live mode rides it as a `?max=<n>` query hint; a server that
   * does not (yet) downscale serves the full-res file unchanged.
   */
  artifactUrl(handle: string, maxDim?: number): string;

  /**
   * Resolve a URL for a media handle's sibling poster still (`<stem>.poster.png`
   * beside the media) — the fixed-frame backdrop the annotator's `slidey` path
   * floats its SemanticOverlay over (a slideshow/video has no addressable still).
   * Live mode returns `/artifact/<handle>/poster` (the server serves the sibling
   * by the SAME handle); snapshot mode returns the sibling path under
   * `artifacts/`. Optional: a source without a poster convention omits it, and
   * the annotator falls back to `artifactUrl(handle)`.
   */
  artifactPosterUrl?(handle: string): string;

  // ── Video feedback mode (/review) ──────────────────────────────────────────

  /** Read a video's chapter sidecar (empty array when none). */
  videoChapters(sessionId: string, video: string): Promise<Chapter[]>;
  /**
   * Read the recorded rrweb session events that back a reviewed video, or `[]`
   * when the media is a plain capture with no reconstructed-DOM sidecar. When
   * present, the review surface renders the rrweb Replayer (real reconstructed
   * UI) under the spatial picker instead of the opaque `<video>` element, so a
   * click resolves a REAL app control against the reconstructed DOM (epic shared
   * decision 2 — "rrweb's reconstructed DOM is the pixel↔element bridge"); the
   * intrinsic recording viewport rides alongside as the iframe's pixel space.
   * Optional: a source without recorded sessions (snapshot/artifact) omits it.
   */
  videoEvents?(
    sessionId: string,
    video: string
  ): Promise<{ events: import("./session-capture.js").RrwebEvent[]; width: number; height: number }>;
  /**
   * Read an artifact's `<name>.semantic.json` sidecar — the producer-declared
   * clickable-element envelope (lib/semanticPlugins, mirroring host.SemanticSidecar).
   * LiveSource hits a server endpoint; SnapshotSource reads
   * ./artifacts/<name>.semantic.json. Resolves null when the artifact has no
   * sidecar (the annotator then falls back to the dom_node picker). The caller
   * adapts the envelope into an overlay SemanticMap with the media's natural size
   * (toSemanticMap). Optional: a source without artifacts omits it.
   */
  semanticMap?(
    sessionId: string,
    handle: string
  ): Promise<SemanticSidecar | null>;

  /** Grab a still at t_ms; returns the recorded still's artifact handle. */
  videoFrame(
    sessionId: string,
    video: string,
    tMs: number
  ): Promise<{ handle: string; mime: string; kind: string }>;
  /** Persist + dispatch one structured feedback note. */
  addFeedback(sessionId: string, note: FeedbackNote): Promise<{ ok: boolean }>;
}

/**
 * Factory: chooses SnapshotSource if window.__KITSOKI_SNAPSHOT__ is defined,
 * else LiveSource('/').
 */
export function createDataSource(): DataSource {
  const win = window as Window &
    typeof globalThis & { __KITSOKI_SNAPSHOT__?: unknown };

  if (win.__KITSOKI_SNAPSHOT__ !== undefined) {
    return new SnapshotSource(win.__KITSOKI_SNAPSHOT__);
  }

  return new LiveSource("/");
}
