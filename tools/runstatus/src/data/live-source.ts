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

/** One SSE frame from /rpc/meta-stream. */
export interface MetaStreamEvent {
  type: "delta" | "tool" | "done" | "error";
  // delta
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

/** One SSE frame from /rpc/turn-stream. */
export interface TurnStreamEvent {
  type: "delta" | "tool" | "done" | "error";
  // delta
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
}

// Re-export for components that import AnnotationEntry from this module.
export type { AnnotationEntry };
