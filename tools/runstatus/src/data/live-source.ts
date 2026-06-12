import type {
  SessionHeader,
  AppDef,
  MermaidSnapshot,
  TraceEvent,
  TurnResult,
  AnnotationEntry,
  ReplayResult,
} from "../types.js";
import type {
  DataSource,
  TraceCursor,
  MetaModeInfo,
  MetaSession,
  MetaSendResult,
  MetaMessage,
} from "./source.js";
import type { TranscriptData, TranscriptEvent } from "./transcript.js";
import type {
  RoomSummary as EditorRoomSummary,
  RoomDetail as EditorRoomDetail,
  OraclesResult as EditorOraclesResult,
  CassetteKey as EditorCassetteKey,
  CassetteEpisodeSummary as EditorCassetteEpisode,
  ReplayResult as EditorReplayResult,
} from "./editor.js";

/** One SSE frame from /rpc/meta-stream. "think" carries extended-thinking
 * prose (always intermediate); "delta" carries narration that may turn out
 * to be the final reply — the client defers it (see stores/meta.ts). */
export interface MetaStreamEvent {
  type: "think" | "delta" | "tool" | "done" | "error";
  // think / delta
  text?: string;
  // tool
  tool?: string;
  preview?: string;
  // done (mirrors MetaSendResult)
  assistant?: string;
  chat_id?: string;
  reload_requested?: boolean;
  changed_files?: string[];
  // error
  message?: string;
}

/** One SSE frame from /rpc/turn-stream. Same think/delta split as
 * MetaStreamEvent; the main chat treats both as feed material. */
export interface TurnStreamEvent {
  type: "think" | "delta" | "tool" | "done" | "error";
  // think / delta
  text?: string;
  // tool
  tool?: string;
  preview?: string;
  // done — carries the full TurnResult
  result?: TurnResult;
  // error
  message?: string;
}
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

/**
 * One notification as it rides the wire. `jobs.Notification` has NO json tags,
 * so it serializes with Go's PascalCase field names — read these EXACTLY (not
 * camelCase, not snake_case). Applies to both the notifications.list array and
 * the SSE `runstatus.notification` frame's `notification` object.
 */
export interface Notification {
  ID: string;
  SessionID: string;
  CreatedAt: string; // RFC3339 (time.Time marshals to a string)
  Severity: "info" | "success" | "warn" | "error" | "action_required";
  Title: string;
  Body: string;
  TeleportState: string;
  TeleportSlots: Record<string, unknown> | null;
  TeleportProposalID: string;
  TeleportJobID: string;
  OriginKind: string;
  OriginRef: string;
  // May be null/zero on the wire.
  ReadAt?: string | null;
  DismissedAt?: string | null;
  SnoozedUntil?: string | null;
  OriginURL?: string | null;
}

/** One SSE frame from /rpc/notifications (the cross-session global feed). */
export interface NotificationFrame {
  session_id: string;
  notification: Notification;
  unread: number;
  needs_attention: number;
}

/**
 * One choice in an operator question (mcp `OperatorAskOption`). json-tagged on
 * the wire, so these are the literal field names the backend emits.
 */
export interface OperatorQuestionOption {
  label: string;
  description?: string;
}

/**
 * One forwarded question (mcp `OperatorAskQuestion`). Mirrors the
 * AskUserQuestion shape a dispatched agent would otherwise have called — the
 * agent's question is forwarded into kitsoki and surfaced to the operator here.
 */
export interface OperatorQuestion {
  question: string;
  header: string;
  multiSelect?: boolean;
  options: OperatorQuestionOption[];
}

/**
 * One SSE frame from /rpc/questions (the per-operator forwarded-question feed).
 * question_id is the token answerQuestion echoes back to unblock the parked
 * oracle; questions is the full set the agent asked in one call.
 */
export interface OperatorQuestionFrame {
  session_id: string;
  question_id: string;
  questions: OperatorQuestion[];
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
  private readonly base: string;

  constructor(base = "/") {
    this.base = base.endsWith("/") ? base : base + "/";
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

  /**
   * Stream one turn via SSE. Calls onEvent for each "delta"/"tool" frame as
   * the oracle generates output; resolves with the final TurnResult when the
   * "done" frame arrives, or rejects on "error" or network failure.
   */
  async turnStream(
    sessionId: string,
    method: "turn" | "submit" | "continue" | "offpath",
    params: { input?: string; intent?: string; slots?: Record<string, unknown> },
    onEvent: (ev: TurnStreamEvent) => void
  ): Promise<TurnResult> {
    const resp = await fetch(`${this.base}rpc/turn-stream`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        session_id: sessionId,
        method,
        ...params,
      }),
    });
    if (!resp.ok) {
      throw new Error(`turn-stream: HTTP ${resp.status} ${resp.statusText}`);
    }
    if (!resp.body) {
      throw new Error("turn-stream: no response body");
    }

    const reader = resp.body.getReader();
    const dec = new TextDecoder();
    let buf = "";
    let finalResult: TurnResult | null = null;

    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      const lines = buf.split("\n");
      buf = lines.pop() ?? "";
      for (const line of lines) {
        if (!line.startsWith("data: ")) continue;
        const raw = line.slice(6).trim();
        if (!raw) continue;
        const ev: TurnStreamEvent = JSON.parse(raw);
        if (ev.type === "done") {
          finalResult = ev.result ?? null;
        } else if (ev.type === "error") {
          throw new Error(ev.message ?? "turn-stream error");
        } else {
          onEvent(ev);
        }
      }
    }

    if (!finalResult) throw new Error("turn-stream: ended without done event");
    return finalResult;
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

  /**
   * Stream one meta turn via SSE. Calls onEvent for each "delta"/"tool" frame
   * as the LLM generates output; resolves with the final MetaSendResult when
   * the "done" frame arrives, or rejects on "error" or network failure.
   */
  async metaStream(
    sessionId: string,
    mode: string,
    chatId: string,
    input: string,
    onEvent: (ev: MetaStreamEvent) => void
  ): Promise<MetaSendResult> {
    const resp = await fetch(`${this.base}rpc/meta-stream`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        session_id: sessionId,
        mode,
        chat_id: chatId,
        input,
      }),
    });
    if (!resp.ok) {
      throw new Error(`meta-stream: HTTP ${resp.status} ${resp.statusText}`);
    }
    if (!resp.body) {
      throw new Error("meta-stream: no response body");
    }

    const reader = resp.body.getReader();
    const dec = new TextDecoder();
    let buf = "";
    let finalResult: MetaSendResult | null = null;

    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      buf += dec.decode(value, { stream: true });
      const lines = buf.split("\n");
      buf = lines.pop() ?? "";
      for (const line of lines) {
        if (!line.startsWith("data: ")) continue;
        const raw = line.slice(6).trim();
        if (!raw) continue;
        const ev: MetaStreamEvent = JSON.parse(raw);
        if (ev.type === "done") {
          finalResult = {
            assistant: ev.assistant ?? "",
            chat_id: ev.chat_id ?? "",
            reload_requested: ev.reload_requested ?? false,
            changed_files: ev.changed_files ?? [],
          };
        } else if (ev.type === "error") {
          throw new Error(ev.message ?? "meta-stream error");
        } else {
          onEvent(ev);
        }
      }
    }

    if (!finalResult) throw new Error("meta-stream: ended without done event");
    return finalResult;
  }

  metaTranscript(sessionId: string, chatId: string): Promise<MetaMessage[]> {
    return this.client
      .post<{ messages: MetaMessage[] }>("runstatus.meta.transcript", {
        session_id: sessionId,
        chat_id: chatId,
      })
      .then((r) => r.messages ?? []);
  }

  // ── Agent-action transcripts ──────────────────────────────────────────────

  /**
   * Fetch one oracle call's agent-action transcript via
   * runstatus.session.transcript. The server reads the <call_id>.jsonl +
   * .timings sidecars lazily off disk and returns the verbatim events parsed
   * back to JSON. Maps the Go snake_case `schema_version` to TS `schemaVersion`.
   */
  getTranscript(sessionId: string, callId: string): Promise<TranscriptData> {
    return this.client
      .post<{
        format: string;
        events: TranscriptEvent[];
        timings: number[];
        schema_version: number;
      }>("runstatus.session.transcript", {
        session_id: sessionId,
        call_id: callId,
      })
      .then((r) => ({
        format: r.format,
        events: r.events ?? [],
        timings: r.timings ?? [],
        schemaVersion: r.schema_version,
      }));
  }

  // ── Media artifacts ───────────────────────────────────────────────────────

  /**
   * Returns the server-side artifact URL for the given handle. The Go server
   * exposes `GET /artifact/<handle>` which validates path traversal and serves
   * the file via http.ServeContent (ETag, Range, Content-Type).
   */
  artifactUrl(handle: string): string {
    // Handles are content-addressed (e.g. "mockup-video#6e2b0759") and the '#'
    // is a URL fragment delimiter — left raw it truncates the path and the
    // server 404s. Encode the handle so '#' rides as %23 into the path segment.
    return `/artifact/${encodeURIComponent(handle)}`;
  }

  // ── Video feedback mode (/review) ──────────────────────────────────────────

  async videoChapters(
    sessionId: string,
    video: string
  ): Promise<import("./source.js").Chapter[]> {
    const res = await this.client.post<{
      chapters: import("./source.js").Chapter[];
    }>("runstatus.video.chapters", { session_id: sessionId, video });
    return res.chapters ?? [];
  }

  videoFrame(
    sessionId: string,
    video: string,
    tMs: number
  ): Promise<{ handle: string; mime: string; kind: string }> {
    return this.client.post("runstatus.video.frame", {
      session_id: sessionId,
      video,
      t_ms: tMs,
    });
  }

  addFeedback(
    sessionId: string,
    note: import("./source.js").FeedbackNote
  ): Promise<{ ok: boolean }> {
    return this.client.post("runstatus.feedback.add", {
      session_id: sessionId,
      ...note,
    });
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

  // ── Story editor (per-story static reads; no session) ─────────────────────

  /** BFS-ordered room list for a story (runstatus.editor.rooms). */
  editorRooms(storyPath: string): Promise<EditorRoomSummary[]> {
    return this.client
      .post<{ rooms: EditorRoomSummary[] }>("runstatus.editor.rooms", {
        story_path: storyPath,
      })
      .then((r) => r.rooms ?? []);
  }

  /** Full detail for one room (runstatus.editor.room). */
  editorRoom(storyPath: string, roomId: string): Promise<EditorRoomDetail> {
    return this.client.post<EditorRoomDetail>("runstatus.editor.room", {
      story_path: storyPath,
      room_id: roomId,
    });
  }

  /** Oracle contracts + cassette globs for a room (runstatus.editor.oracles). */
  editorOracles(
    storyPath: string,
    roomId: string
  ): Promise<EditorOraclesResult> {
    return this.client.post<EditorOraclesResult>("runstatus.editor.oracles", {
      story_path: storyPath,
      room_id: roomId,
    });
  }

  /** Cassette episodes matching a key (runstatus.editor.cassettes). */
  editorCassettes(
    storyPath: string,
    cassetteKey: EditorCassetteKey
  ): Promise<EditorCassetteEpisode[]> {
    return this.client
      .post<{ episodes: EditorCassetteEpisode[] }>(
        "runstatus.editor.cassettes",
        { story_path: storyPath, cassette_key: cassetteKey }
      )
      .then((r) => r.episodes ?? []);
  }

  /** Cassette-override replay of an oracle call (runstatus.editor.replay). */
  editorReplay(
    storyPath: string,
    roomId: string,
    oracleIndex: number,
    cassetteFile?: string
  ): Promise<EditorReplayResult> {
    const params: Record<string, unknown> = {
      story_path: storyPath,
      room_id: roomId,
      oracle_index: oracleIndex,
    };
    if (cassetteFile) params["cassette_file"] = cassetteFile;
    return this.client.post<EditorReplayResult>(
      "runstatus.editor.replay",
      params
    );
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

  /**
   * Check whether the session's app.yaml on disk has changed since it was
   * loaded (or last reloaded). `stale` is true when they differ; `diff` is a
   * unified-diff string suitable for display in a modal.
   */
  checkStaleness(
    sessionId: string
  ): Promise<{ stale: boolean; diff: string }> {
    return this.client.post<{ stale: boolean; diff: string }>(
      "runstatus.session.staleness",
      { session_id: sessionId }
    );
  }

  /**
   * Add an operator annotation (score / label / comment) to the session's
   * annotation sidecar. Either targetCallId or targetTurn should be supplied
   * to identify what is being annotated.
   */
  addAnnotation(
    sessionId: string,
    params: {
      targetCallId?: string;
      targetTurn?: number;
      score?: number;
      label?: string;
      comment?: string;
      annotator?: string;
    }
  ): Promise<{ ok: boolean }> {
    const body: Record<string, unknown> = { session_id: sessionId };
    if (params.targetCallId !== undefined) body["target_call_id"] = params.targetCallId;
    if (params.targetTurn !== undefined) body["target_turn"] = params.targetTurn;
    if (params.score !== undefined) body["score"] = params.score;
    if (params.label !== undefined) body["label"] = params.label;
    if (params.comment !== undefined) body["comment"] = params.comment;
    if (params.annotator !== undefined) body["annotator"] = params.annotator;
    return this.client.post<{ ok: boolean }>("runstatus.annotation.add", body);
  }

  /**
   * Replay one recorded oracle call against a chosen operator.
   * In v1 the re-dispatch is a stub (no live LLM call); the result confirms
   * replayability and carries a note. new_verdict and diff will be populated
   * once the live dispatch path is wired.
   */
  replayCall(
    sessionId: string,
    callId: string,
    operator: "claude" | "local"
  ): Promise<ReplayResult> {
    return this.client.post<ReplayResult>("runstatus.call.replay", {
      session_id: sessionId,
      call_id: callId,
      operator,
    });
  }

  // ── Inbox / notifications ─────────────────────────────────────────────────
  //
  // The list/read/dismiss RPCs are per-session; the SUBSCRIBE feed is global
  // (cross-session, no session_id) and is the primary source of truth for the
  // web inbox store. read/dismiss do NOT push a refreshed-count SSE frame, so
  // the store reconciles counts optimistically client-side.

  /** List a session's notifications (most recent first), newest `limit` items. */
  listNotifications(
    sessionId: string,
    limit?: number
  ): Promise<Notification[]> {
    const params: Record<string, unknown> = { session_id: sessionId };
    if (limit !== undefined) params["limit"] = limit;
    return this.client
      .post<{ notifications: Notification[] }>(
        "runstatus.session.notifications.list",
        params
      )
      .then((r) => r.notifications ?? []);
  }

  /** Mark one notification read. */
  readNotification(sessionId: string, id: string): Promise<{ ok: boolean }> {
    return this.client.post<{ ok: boolean }>(
      "runstatus.session.notifications.read",
      { session_id: sessionId, id }
    );
  }

  /** Dismiss one notification (removed from the active list, kept in history). */
  dismissNotification(
    sessionId: string,
    id: string
  ): Promise<{ ok: boolean }> {
    return this.client.post<{ ok: boolean }>(
      "runstatus.session.notifications.dismiss",
      { session_id: sessionId, id }
    );
  }

  /**
   * Teleport a session to the room a notification points at. Returns a
   * TurnResult (same shape as session.turn) so the caller can apply it to the
   * run store. A non-teleportable / unknown id rejects with JSON-RPC -32000.
   */
  teleport(sessionId: string, notificationId: string): Promise<TurnResult> {
    return this.client.post<TurnResult>("runstatus.session.teleport", {
      session_id: sessionId,
      notification_id: notificationId,
    });
  }

  /**
   * Subscribe to the GLOBAL notification feed (a second EventSource on
   * /rpc/notifications, separate from the per-session /rpc/events stream).
   * Mirrors the per-session subscribe lifecycle: subscribe → open stream →
   * reconnect with exponential backoff → unsubscribe on teardown. Returns an
   * unsubscribe function.
   */
  subscribeNotifications(
    onFrame: (frame: NotificationFrame) => void,
    onError?: (e: unknown) => void
  ): () => void {
    let subscriptionId = "";
    let es: EventSource | null = null;
    let closed = false;
    let backoffAttempt = 0;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

    const openStream = () => {
      if (closed) return;
      const url = `${this.base}rpc/notifications?subscription_id=${encodeURIComponent(subscriptionId)}`;
      es = new EventSource(url);

      es.onmessage = (ev: MessageEvent<string>) => {
        backoffAttempt = 0;
        try {
          const frame = JSON.parse(ev.data) as {
            method?: string;
            params?: NotificationFrame;
          };
          if (frame.method === "runstatus.notification" && frame.params) {
            onFrame(frame.params);
          }
        } catch {
          // Malformed frame — ignore.
        }
      };

      es.onerror = (e) => {
        if (closed) return;
        onError?.(e);
        es?.close();
        es = null;
        const delay = NOTIF_BACKOFF_MS[
          Math.min(backoffAttempt++, NOTIF_BACKOFF_MS.length - 1)
        ] ?? 5000;
        reconnectTimer = setTimeout(() => {
          if (!closed) openStream();
        }, delay);
      };
    };

    this.client
      .post<{ subscription_id: string }>("runstatus.notifications.subscribe", {})
      .then(({ subscription_id }) => {
        if (closed) return;
        subscriptionId = subscription_id;
        openStream();
      })
      .catch((e) => onError?.(e));

    return () => {
      closed = true;
      if (reconnectTimer !== null) clearTimeout(reconnectTimer);
      es?.close();
      es = null;
      if (subscriptionId) {
        this.client
          .post("runstatus.notifications.unsubscribe", {
            subscription_id: subscriptionId,
          })
          .catch(() => undefined);
      }
    };
  }

  /**
   * Subscribe to the forwarded-question feed (a third EventSource on
   * /rpc/questions). When a dispatched agent forwards an AskUserQuestion into
   * kitsoki, the parked oracle turn blocks until the operator answers; a frame
   * lands here so the SPA can surface the modal. Same subscribe → stream →
   * backoff → unsubscribe lifecycle as the notification feed. Returns an
   * unsubscribe function.
   */
  subscribeQuestions(
    onFrame: (frame: OperatorQuestionFrame) => void,
    onError?: (e: unknown) => void
  ): () => void {
    let subscriptionId = "";
    let es: EventSource | null = null;
    let closed = false;
    let backoffAttempt = 0;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

    const openStream = () => {
      if (closed) return;
      const url = `${this.base}rpc/questions?subscription_id=${encodeURIComponent(subscriptionId)}`;
      es = new EventSource(url);

      es.onmessage = (ev: MessageEvent<string>) => {
        backoffAttempt = 0;
        try {
          const frame = JSON.parse(ev.data) as {
            method?: string;
            params?: OperatorQuestionFrame;
          };
          if (frame.method === "runstatus.question" && frame.params) {
            onFrame(frame.params);
          }
        } catch {
          // Malformed frame — ignore.
        }
      };

      es.onerror = (e) => {
        if (closed) return;
        onError?.(e);
        es?.close();
        es = null;
        const delay = NOTIF_BACKOFF_MS[
          Math.min(backoffAttempt++, NOTIF_BACKOFF_MS.length - 1)
        ] ?? 5000;
        reconnectTimer = setTimeout(() => {
          if (!closed) openStream();
        }, delay);
      };
    };

    this.client
      .post<{ subscription_id: string }>("runstatus.questions.subscribe", {})
      .then(({ subscription_id }) => {
        if (closed) return;
        subscriptionId = subscription_id;
        openStream();
      })
      .catch((e) => onError?.(e));

    return () => {
      closed = true;
      if (reconnectTimer !== null) clearTimeout(reconnectTimer);
      es?.close();
      es = null;
      if (subscriptionId) {
        this.client
          .post("runstatus.questions.unsubscribe", {
            subscription_id: subscriptionId,
          })
          .catch(() => undefined);
      }
    };
  }

  /**
   * Answer a forwarded question, unblocking the parked oracle turn. answers is
   * keyed by each question's text; the value is the chosen option label
   * (single-select) or an array of labels (multiSelect) — the same shape
   * AskUserQuestion would have returned to the agent.
   */
  answerQuestion(
    questionId: string,
    answers: Record<string, string | string[]>
  ): Promise<{ ok: boolean }> {
    return this.client.post<{ ok: boolean }>(
      "runstatus.session.answer_question",
      { question_id: questionId, answers }
    );
  }
}

// Backoff schedule for the notification feed reconnect (ms).
const NOTIF_BACKOFF_MS = [250, 500, 1000, 2000, 5000];

// Re-export for components that import AnnotationEntry from this module.
export type { AnnotationEntry };
