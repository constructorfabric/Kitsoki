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
import { SnapshotSource } from "./snapshot-source.js";
import { LiveSource } from "./live-source.js";

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
  key: string; // "story.edit" | "story.ask" | "kitsoki.ask" | …
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
    slots?: Record<string, unknown>
  ): Promise<TurnResult>;
  /** Free-text turn: hand raw input to the interpreter to pick an intent. */
  sendTurn(sessionId: string, input: string): Promise<TurnResult>;
  /** Supply missing slots to a clarifying turn and continue. */
  continueTurn(
    sessionId: string,
    slots: Record<string, unknown>
  ): Promise<TurnResult>;
  /** Read-only off-path question against the default agent. */
  offpath(sessionId: string, input: string): Promise<{ answer: string }>;

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
   */
  artifactUrl(handle: string): string;

  // ── Video feedback mode (/review) ──────────────────────────────────────────

  /** Read a video's chapter sidecar (empty array when none). */
  videoChapters(sessionId: string, video: string): Promise<Chapter[]>;
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
