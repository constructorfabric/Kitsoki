import type {
  SessionHeader,
  AppDef,
  MermaidSnapshot,
  TraceEvent,
  TurnResult,
} from "../types.js";
import type {
  DataSource,
  TraceCursor,
  MetaModeInfo,
  MetaSession,
  MetaSendResult,
  MetaMessage,
} from "./source.js";
import { JsonRpcClient } from "../transport/jsonrpc.js";

/**
 * StoryHeader is one discovered story as the home-screen browser renders it.
 * It mirrors `server.StoryHeader` (internal/runstatus/server/provider.go):
 *
 *   - path is the ABSOLUTE path to the story's app.yaml — the canonical key
 *     session.new takes; app_id is display-only.
 *   - active_sessions lists the ids of live sessions started from this story.
 */
export interface StoryHeader {
  path: string;
  app_id: string;
  title: string;
  active_sessions: string[];
}

/** Result of runstatus.session.reload — mirrors Orchestrator.Reload semantics. */
export interface ReloadResult {
  ok: boolean;
  prev_state_exists: boolean;
}

/**
 * DataSource backed by the kitsoki HTTP JSON-RPC + SSE endpoint.
 */
export class LiveSource implements DataSource {
  private readonly client: JsonRpcClient;

  constructor(base = "/") {
    this.client = new JsonRpcClient(base);
  }

  listSessions(): Promise<SessionHeader[]> {
    return this.client.post<SessionHeader[]>("runstatus.sessions.list", {});
  }

  getSession(sessionId: string): Promise<SessionHeader> {
    return this.client.post<SessionHeader>("runstatus.session.get", {
      session_id: sessionId,
    });
  }

  getApp(sessionId: string): Promise<AppDef> {
    return this.client.post<AppDef>("runstatus.session.app", {
      session_id: sessionId,
    });
  }

  getMermaid(sessionId: string, detail?: string): Promise<MermaidSnapshot> {
    const params: Record<string, unknown> = { session_id: sessionId };
    if (detail !== undefined) params["detail"] = detail;
    return this.client.post<MermaidSnapshot>(
      "runstatus.session.mermaid",
      params
    );
  }

  getTrace(
    sessionId: string,
    cursor?: TraceCursor
  ): Promise<{ events: TraceEvent[]; last_turn: number }> {
    const params: Record<string, unknown> = { session_id: sessionId };
    if (cursor?.since_turn !== undefined)
      params["since_turn"] = cursor.since_turn;
    if (cursor?.until_turn !== undefined)
      params["until_turn"] = cursor.until_turn;
    if (cursor?.limit !== undefined) params["limit"] = cursor.limit;
    return this.client.post<{ events: TraceEvent[]; last_turn: number }>(
      "runstatus.session.trace",
      params
    );
  }

  subscribe(
    sessionId: string,
    onEvent: (e: TraceEvent) => void
  ): () => void {
    return this.client.subscribe(sessionId, onEvent, (sinceТurn) =>
      this.getTrace(sessionId, { since_turn: sinceТurn })
    );
  }

  // ── Write/read RPCs ────────────────────────────────────────────────────
  //
  // The live server hosts a single in-process session, so the write/read RPCs
  // take no session_id (the engine resolves the one live session). We still
  // pass session_id for parity with the read RPCs; the server ignores it.

  view(sessionId: string): Promise<TurnResult> {
    return this.client.post<TurnResult>("runstatus.session.view", {
      session_id: sessionId,
    });
  }

  submit(
    sessionId: string,
    intent: string,
    slots: Record<string, unknown> = {}
  ): Promise<TurnResult> {
    return this.client.post<TurnResult>("runstatus.session.submit", {
      session_id: sessionId,
      intent,
      slots,
    });
  }

  sendTurn(sessionId: string, input: string): Promise<TurnResult> {
    return this.client.post<TurnResult>("runstatus.session.turn", {
      session_id: sessionId,
      input,
    });
  }

  continueTurn(
    sessionId: string,
    slots: Record<string, unknown>
  ): Promise<TurnResult> {
    return this.client.post<TurnResult>("runstatus.session.continue", {
      session_id: sessionId,
      slots,
    });
  }

  offpath(sessionId: string, input: string): Promise<{ answer: string }> {
    return this.client.post<{ answer: string }>("runstatus.session.offpath", {
      session_id: sessionId,
      input,
    });
  }

  // ── Meta mode (overlay chat) ─────────────────────────────────────────────

  metaModes(sessionId: string): Promise<MetaModeInfo[]> {
    return this.client
      .post<{ modes: MetaModeInfo[] }>("runstatus.meta.modes", {
        session_id: sessionId,
      })
      .then((r) => r.modes ?? []);
  }

  metaEnter(
    sessionId: string,
    mode: string,
    chatId = ""
  ): Promise<MetaSession> {
    return this.client.post<MetaSession>("runstatus.meta.enter", {
      session_id: sessionId,
      mode,
      chat_id: chatId,
    });
  }

  metaSend(
    sessionId: string,
    mode: string,
    chatId: string,
    input: string
  ): Promise<MetaSendResult> {
    return this.client.post<MetaSendResult>("runstatus.meta.send", {
      session_id: sessionId,
      mode,
      chat_id: chatId,
      input,
    });
  }

  metaNew(
    sessionId: string,
    mode: string,
    chatId: string
  ): Promise<MetaSession> {
    return this.client.post<MetaSession>("runstatus.meta.new", {
      session_id: sessionId,
      mode,
      chat_id: chatId,
    });
  }

  metaTranscript(sessionId: string, chatId: string): Promise<MetaMessage[]> {
    return this.client
      .post<{ messages: MetaMessage[] }>("runstatus.meta.transcript", {
        session_id: sessionId,
        chat_id: chatId,
      })
      .then((r) => r.messages ?? []);
  }

  // ── Media artifacts ───────────────────────────────────────────────────────

  /**
   * Returns the server-side artifact URL for the given handle. The Go server
   * exposes `GET /artifact/<handle>` which validates path traversal and serves
   * the file via http.ServeContent (ETag, Range, Content-Type).
   */
  artifactUrl(handle: string): string {
    return `/artifact/${handle}`;
  }

  // ── Multi-story lifecycle RPCs ───────────────────────────────────────────
  //
  // These drive the home screen (story browser + live-session list +
  // new-session) and the per-session Reload action. They are session-agnostic
  // (stories.*/session.new) or take an explicit session_id (session.reload)
  // rather than relying on a single in-process session.

  /** List the discovered story catalogue. */
  listStories(): Promise<StoryHeader[]> {
    return this.client.post<StoryHeader[]>("runstatus.stories.list", {});
  }

  /** Re-scan the configured story directories and return the fresh catalogue. */
  rescanStories(): Promise<StoryHeader[]> {
    return this.client.post<StoryHeader[]>("runstatus.stories.rescan", {});
  }

  /**
   * Start a new session from a story's app.yaml path. Returns the new
   * session id; the server fails fast with a structured error on an invalid
   * story so the UI can surface it before navigating.
   */
  newSession(storyPath: string): Promise<string> {
    return this.client
      .post<{ session_id: string }>("runstatus.session.new", {
        story_path: storyPath,
      })
      .then((r) => r.session_id);
  }

  /**
   * Reload a session's story definition in place, mirroring the TUI /reload.
   * `prev_state_exists:false` means the session's current state was removed by
   * the edit, so the engine stays put rather than advancing.
   */
  reloadSession(sessionId: string): Promise<ReloadResult> {
    return this.client.post<ReloadResult>("runstatus.session.reload", {
      session_id: sessionId,
    });
  }
}
