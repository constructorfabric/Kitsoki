import type {
  SessionHeader,
  AppDef,
  MermaidSnapshot,
  TraceEvent,
  TurnResult,
} from "../types.js";
import { SnapshotSource } from "./snapshot-source.js";
import { LiveSource } from "./live-source.js";

export interface TraceCursor {
  since_turn?: number;
  until_turn?: number;
  limit?: number;
}

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

export interface DataSource {
  listSessions(): Promise<SessionHeader[]>;
  getSession(sessionId: string): Promise<SessionHeader>;
  getApp(sessionId: string): Promise<AppDef>;
  getMermaid(sessionId: string, detail?: string): Promise<MermaidSnapshot>;
  getTrace(
    sessionId: string,
    cursor?: TraceCursor
  ): Promise<{ events: TraceEvent[]; last_turn: number }>;
  /** Returns an unsubscribe function. */
  subscribe(sessionId: string, onEvent: (e: TraceEvent) => void): () => void;

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
  /** Read-only off-path question against the default oracle. */
  offpath(sessionId: string, input: string): Promise<{ answer: string }>;

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

  // ── Media artifacts ───────────────────────────────────────────────────────

  /**
   * Resolve a URL for a named artifact handle. In live mode returns the
   * server-side `/artifact/<handle>` path; in snapshot mode returns a
   * relative sidecar path `./artifacts/<handle>` (the handle is the
   * filename under the snapshot's sibling `artifacts/` directory).
   */
  artifactUrl(handle: string): string;
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
