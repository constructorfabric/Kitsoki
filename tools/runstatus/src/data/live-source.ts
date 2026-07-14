import type {
  SessionHeader,
  AppDef,
  MermaidSnapshot,
  TraceEvent,
  TurnResult,
  AnnotationEntry,
  ReplayResult,
  HarnessState,
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
  AgentsResult as EditorAgentsResult,
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
  type: "think" | "delta" | "tool" | "routing" | "done" | "cancelled" | "error";
  // think / delta
  text?: string;
  // tool
  tool?: string;
  preview?: string;
  // done — carries the full TurnResult
  result?: TurnResult;
  // error
  message?: string;
  // routing
  turn?: number;
  intent?: string;
  routed_by?: string;
  match_type?: string;
  confidence?: number;
}

/**
 * Thrown by {@link LiveSource.turnStream} when the turn ends with a "cancelled"
 * frame — i.e. the operator hit Stop and runstatus.session.cancel aborted the
 * turn server-side. Distinct from a transport/agent error so callers can reset
 * to idle WITHOUT surfacing a red error: nothing was persisted, the session is
 * untouched at its pre-turn state.
 */
export class TurnCancelledError extends Error {
  constructor() {
    super("turn cancelled");
    this.name = "TurnCancelledError";
  }
}
import { JsonRpcClient } from "../transport/jsonrpc.js";
import type { LastRpcError } from "../transport/jsonrpc.js";
import { serializeAnchor } from "../lib/annotationAnchor.js";
import { createTransport } from "../transport/transport.js";
import type { RpcTransport } from "../transport/transport.js";

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

export interface WorkSummary {
  items: number;
  needs_attention: number;
  jobs_running: number;
  jobs_awaiting_input: number;
  jobs_terminal: number;
  notifications_unread: number;
  notifications_action_required: number;
  pending_drives: number;
  dispatching_drives?: number;
  failed_drives?: number;
  backgrounded_chats: number;
  operator_questions?: number;
  mining_proposals?: number;
}

export interface WorkSession {
  session_id: string;
  app_id?: string;
  current_state?: string;
  work: WorkSummary;
}

export interface WorkItem {
  kind: "notification" | "job" | string;
  priority: number;
  session_id: string;
  title?: string;
  body?: string;
  status?: string;
  notification_id?: string;
  job_id?: string;
  severity?: Notification["Severity"];
  created_at?: string;
  updated_at?: string;
  read_at?: string | null;
  teleport_state?: string;
  teleport_slots?: Record<string, unknown> | null;
  teleport_job_id?: string;
  origin_kind?: string;
  origin_ref?: string;
  origin_url?: string;
  origin_state?: string;
  reacquire_tool: "notification" | "session" | "chat.show" | string;
  reacquire_session_id?: string;
  drive_id?: string;
  chat_id?: string;
  question_id?: string;
  proposal_id?: string;
  proposal_kind?: string;
  proposal_target?: string;
  draft_path?: string;
  rung?: number;
  questions?: OperatorQuestion[];
  actor?: string;
  thread?: string;
  tmux_session?: string;
  tmux_host?: string;
}

export interface WorkListResult {
  summary: WorkSummary;
  sessions: WorkSession[];
  items: WorkItem[];
}

export interface ChatInspectItem {
  id: string;
  app_id: string;
  room: string;
  scope_key: string;
  display_scope_key?: string;
  title: string;
  status: string;
  claude_session_id?: string;
  parent_chat_id?: string;
  session_id?: string;
  created_at_unix_micro: number;
  updated_at_unix_micro: number;
  last_active_at_unix_micro: number;
}

export interface ChatPTYItem {
  chat_id: string;
  tmux_session: string;
  tmux_host: string;
  mode: string;
  permission_mode?: string;
  workspace_path?: string;
  created_at_unix_micro: number;
  updated_at_unix_micro: number;
  last_idle_at_unix_micro?: number;
}

export interface ChatMessageItem {
  chat_id: string;
  seq: number;
  role: string;
  content: string;
  metadata?: Record<string, unknown>;
  created_at_unix_micro: number;
}

export interface ChatShowContext {
  session_id?: string;
}

export interface ChatShowResult {
  ok: boolean;
  context?: ChatShowContext;
  chat: ChatInspectItem;
  pty?: ChatPTYItem;
  messages?: ChatMessageItem[];
}

export interface GitHubInboxSyncItem {
  notification_id: string;
  kind: "issue" | "pr" | string;
  number: string;
  title: string;
  url?: string;
  inserted: boolean;
  origin_ref: string;
  teleport_state: string;
  teleport_slots?: Record<string, unknown>;
}

export interface GitHubInboxSyncResult {
  ok: boolean;
  session_id: string;
  fetched: number;
  inserted: number;
  skipped: number;
  items: GitHubInboxSyncItem[];
}

export interface GitHubInboxSyncOptions {
  repo?: string;
  include_issues?: boolean;
  include_prs?: boolean;
  assignee?: string;
  review_requested?: string;
  limit?: number;
  teleport_state?: string;
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
 * agent; questions is the full set the agent asked in one call.
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
  private readonly transport: RpcTransport;

  /**
   * @param base      JSON-RPC endpoint base (default "/").
   * @param transport optional injected transport. Defaults to
   *                  createTransport(base) — HttpTransport in a browser tab,
   *                  BridgeTransport inside a VS Code webview — so every existing
   *                  `new LiveSource("/")` call site bridges transparently.
   */
  constructor(base = "/", transport: RpcTransport = createTransport(base)) {
    this.transport = transport;
    this.client = new JsonRpcClient(base, transport);
  }

  listSessions(): Promise<SessionHeader[]> {
    return this.client.post<SessionHeader[]>("runstatus.sessions.list", {});
  }

  listWork(): Promise<WorkListResult> {
    return this.client.post<WorkListResult>("runstatus.work.list", {});
  }

  showChat(sessionId: string, chatId: string, sinceSeq = 0): Promise<ChatShowResult> {
    return this.client.post<ChatShowResult>("runstatus.chat.show", {
      session_id: sessionId,
      chat_id: chatId,
      since_seq: sinceSeq,
    });
  }

  syncGitHubInbox(
    sessionId: string,
    opts: GitHubInboxSyncOptions = {}
  ): Promise<GitHubInboxSyncResult> {
    return this.client.post<GitHubInboxSyncResult>(
      "runstatus.session.inbox.sync_github",
      { session_id: sessionId, ...opts }
    );
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
    onEvent: (e: TraceEvent) => void,
    onConnectionChange?: (state: import("./source.js").ConnectionState) => void
  ): () => void {
    return this.client.subscribe(
      sessionId,
      onEvent,
      (sinceТurn) => this.getTrace(sessionId, { since_turn: sinceТurn }),
      onConnectionChange
    );
  }

  // ── Active-session discovery ─────────────────────────────────────────────
  //
  // Trace-only / graph-only surfaces (no chat) follow the single active session.

  getCurrentSession(): Promise<string | null> {
    return this.client
      .post<{ session_id: string | null }>("runstatus.session.current", {})
      .then((r) => r.session_id ?? null);
  }

  /**
   * Subscribe to current-session changes: open an EventSource on
   * /rpc/session-current and invoke onChange for each runstatus.session.changed
   * frame (the server seeds the latest value on subscribe so a late subscriber
   * syncs at once). Mirrors the notification feed's subscribe → open stream →
   * unsubscribe lifecycle. Returns an unsubscribe disposer.
   */
  subscribeCurrentSession(
    onChange: (sessionId: string | null) => void
  ): () => void {
    let subscriptionId = "";
    let closed = false;
    let unsubStream: (() => void) | null = null;

    const onMessage = (raw: string) => {
      try {
        const frame = JSON.parse(raw) as {
          method?: string;
          params?: { session_id?: string | null };
        };
        if (frame.method === "runstatus.session.changed" && frame.params) {
          onChange(frame.params.session_id ?? null);
        }
      } catch {
        // Malformed frame — ignore.
      }
    };

    this.client
      .post<{ subscription_id: string }>(
        "runstatus.session.current.subscribe",
        {}
      )
      .then(({ subscription_id }) => {
        if (closed) return;
        subscriptionId = subscription_id;
        unsubStream = this.transport.openEventStream(
          "rpc/session-current",
          { subscription_id },
          { onMessage }
        );
      })
      .catch(() => undefined);

    return () => {
      closed = true;
      unsubStream?.();
      unsubStream = null;
      if (subscriptionId) {
        this.client
          .post("runstatus.session.current.unsubscribe", {
            subscription_id: subscriptionId,
          })
          .catch(() => undefined);
      }
    };
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
    slots: Record<string, unknown> = {},
    anchor?: import("../lib/annotationAnchor.js").AnnotationAnchor
  ): Promise<TurnResult> {
    return this.client.post<TurnResult>("runstatus.session.submit", {
      session_id: sessionId,
      intent,
      slots,
      // The media-annotation composer dispatches an intent (e.g. a deck's
      // `refine`) and rides the picked anchor as a top-level param; the server
      // lifts it into host.WithVisualAmbient so the agent edits the pointed-at
      // element. Dropped when no anchor (plain intent submits stay identical).
      ...(serializeAnchorParam(anchor) ? { anchor: serializeAnchorParam(anchor) } : {}),
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

  driveOperation(sessionId: string): Promise<TurnResult> {
    return this.client.post<TurnResult>("runstatus.session.drive_operation", {
      session_id: sessionId,
    });
  }

  offpath(
    sessionId: string,
    input: string,
    visual?: import("./source.js").VisualBundle,
    anchor?: import("../lib/annotationAnchor.js").AnnotationAnchor
  ): Promise<{ answer: string }> {
    return this.client.post<{ answer: string }>("runstatus.session.offpath", {
      session_id: sessionId,
      input,
      // The optional spatial bundle (slice 1 lifts it server-side into
      // host.WithVisualAmbient). The server's element.bbox is a [x,y,w,h]
      // array; flatten the resolver's {x,y,width,height} into it here so the
      // wire shape matches host.VisualAmbient exactly.
      ...(visual ? { visual: visualParams(visual) } : {}),
      // The v2 unified annotation anchor rides alongside (the backend slice
      // lifts the richer discriminated target into the agent ambient). The
      // component anchor is projected to the on-wire AnchorWire shape
      // host.AnchorFromParams decodes (kind + sibling-named target, bbox/path as
      // positional arrays) — UI-only fields are dropped here.
      ...(serializeAnchorParam(anchor) ? { anchor: serializeAnchorParam(anchor) } : {}),
    });
  }

  workflowCreate(
    goal: string,
    slug = ""
  ): Promise<import("./source.js").WorkflowReceipt> {
    return this.client.post<import("./source.js").WorkflowReceipt>(
      "runstatus.workflow.create",
      {
        goal,
        ...(slug ? { slug } : {}),
      }
    );
  }

  workflowValidate(
    workflowId: string
  ): Promise<import("./source.js").WorkflowReceipt> {
    return this.client.post<import("./source.js").WorkflowReceipt>(
      "runstatus.workflow.validate",
      { workflow_id: workflowId }
    );
  }

  workflowLaunch(
    workflowId: string
  ): Promise<import("./source.js").WorkflowReceipt> {
    return this.client.post<import("./source.js").WorkflowReceipt>(
      "runstatus.workflow.launch",
      { workflow_id: workflowId }
    );
  }

  workflowStatus(
    workflowId: string
  ): Promise<import("./source.js").WorkflowReceipt> {
    return this.client.post<import("./source.js").WorkflowReceipt>(
      "runstatus.workflow.status",
      { workflow_id: workflowId }
    );
  }

  workflowExport(
    workflowId: string,
    opts: import("./source.js").WorkflowExportOptions = {}
  ): Promise<import("./source.js").WorkflowReceipt> {
    return this.client.post<import("./source.js").WorkflowReceipt>(
      "runstatus.workflow.export",
      {
        workflow_id: workflowId,
        ...(opts.target ? { target: opts.target } : {}),
        ...(opts.allow_base_story ? { allow_base_story: true } : {}),
      }
    );
  }

  /**
   * Read an artifact's semantic sidecar via runstatus.artifact.semantic. The
   * server resolves `<name>.semantic.json` next to the media and returns the
   * generic element map, or `{ elements: [] }` / a 404-shaped null when there is
   * no sidecar — the annotator then falls back to the dom_node picker.
   */
  async semanticMap(
    sessionId: string,
    handle: string
  ): Promise<import("../lib/semanticPlugins.js").SemanticSidecar | null> {
    const res = await this.client.post<
      import("../lib/semanticPlugins.js").SemanticSidecar | null
    >("runstatus.artifact.semantic", { session_id: sessionId, handle });
    if (!res || !res.elements || res.elements.length === 0) return null;
    return res;
  }

  /**
   * Rewind one CRR decision (the route-receipt chip's reroute affordance):
   * reverse the route at decisionId and re-dispatch the original utterance,
   * optionally under newClass. Resolves with the re-dispatched turn. An
   * intent-class rewind rejects server-side ("not yet implemented") until the
   * journal can recover the accepted intent again.
   */
  rewindRoute(
    sessionId: string,
    decisionId: string,
    newClass?: string,
    reason?: string,
    workspacePath?: string
  ): Promise<TurnResult> {
    return this.client.post<TurnResult>("runstatus.session.rewind_route", {
      session_id: sessionId,
      decision_id: decisionId,
      ...(newClass ? { new_class: newClass } : {}),
      ...(reason ? { reason } : {}),
      ...(workspacePath ? { workspace_path: workspacePath } : {}),
    });
  }

  /**
   * Cancel the in-flight streamed turn for this session (the chat "Stop"
   * button). Aborts the agent server-side — the running turn observes the
   * cancel and stops the agent subprocess — rather than only the frontend. The
   * in-flight turnStream promise rejects with {@link TurnCancelledError} once
   * the server emits its "cancelled" terminal frame. Resolves with
   * cancelled:false when no turn was in flight (idempotent).
   */
  cancelTurn(sessionId: string): Promise<{ cancelled: boolean }> {
    return this.client.post<{ cancelled: boolean }>("runstatus.session.cancel", {
      session_id: sessionId,
    });
  }

  /**
   * Record an operator up/down verdict on a routed turn (the web chat's
   * thumbs-up/down control) via runstatus.session.routing_feedback — the same
   * journaled event the TUI's `/route up|down` command writes.
   */
  async routingFeedback(
    sessionId: string,
    feedback: { state: string; intent: string; phrase: string; tier: string; verdict: "up" | "down" }
  ): Promise<void> {
    await this.client.post<{ ok: boolean }>("runstatus.session.routing_feedback", {
      session_id: sessionId,
      state: feedback.state,
      intent: feedback.intent,
      phrase: feedback.phrase,
      tier: feedback.tier,
      verdict: feedback.verdict,
    });
  }

  getHarness(sessionId: string): Promise<HarnessState> {
    return this.client.post<HarnessState>("runstatus.session.harness", {
      session_id: sessionId,
    });
  }

  setSelection(
    sessionId: string,
    profile: string,
    model?: string,
    effort?: string
  ): Promise<HarnessState> {
    return this.client.post<HarnessState>("runstatus.session.set_selection", {
      session_id: sessionId,
      profile,
      ...(model ? { model } : {}),
      ...(effort ? { effort } : {}),
    });
  }

  /**
   * Stream one turn via SSE. Calls onEvent for each "delta"/"tool" frame as
   * the agent generates output; resolves with the final TurnResult when the
   * "done" frame arrives, or rejects on "error" or network failure.
   */
  turnStream(
    sessionId: string,
    method: "turn" | "submit" | "continue" | "offpath",
    params: {
      input?: string;
      intent?: string;
      slots?: Record<string, unknown>;
      anchor?: import("../lib/annotationAnchor.js").AnnotationAnchor;
    },
    onEvent: (ev: TurnStreamEvent) => void
  ): Promise<TurnResult> {
    // The media-annotation composer rides a picked anchor over the streaming
    // submit; project it to the on-wire shape (the server lifts it into
    // host.WithVisualAmbient). A no-op when no anchor — plain turns are
    // byte-identical.
    const { anchor, ...rest } = params;
    const wireAnchor = serializeAnchorParam(anchor);
    return this.transport.postEventStream<TurnResult>(
      "rpc/turn-stream",
      {
        session_id: sessionId,
        method,
        ...rest,
        ...(wireAnchor ? { anchor: wireAnchor } : {}),
      },
      {
        onFrame: (frame) => onEvent(frame as unknown as TurnStreamEvent),
        reduce: (frame) => {
          const ev = frame as unknown as TurnStreamEvent;
          if (ev.type === "done") {
            // Mirror the original loop's `finalResult = ev.result ?? null`
            // followed by `if (!finalResult) throw "ended without done event"`:
            // a done frame carrying no result is a malformed terminal.
            if (ev.result == null) {
              throw new Error("turn-stream: ended without done event");
            }
            return { result: ev.result };
          }
          if (ev.type === "cancelled") {
            // Operator hit Stop: the server aborted the turn and persisted
            // nothing. Reject with a typed error so the store/view reset to idle
            // without showing a transport error.
            throw new TurnCancelledError();
          }
          if (ev.type === "error") {
            throw new Error(ev.message ?? "turn-stream error");
          }
          return undefined;
        },
      }
    );
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
  metaStream(
    sessionId: string,
    mode: string,
    chatId: string,
    input: string,
    onEvent: (ev: MetaStreamEvent) => void
  ): Promise<MetaSendResult> {
    return this.transport.postEventStream<MetaSendResult>(
      "rpc/meta-stream",
      { session_id: sessionId, mode, chat_id: chatId, input },
      {
        onFrame: (frame) => onEvent(frame as unknown as MetaStreamEvent),
        reduce: (frame) => {
          const ev = frame as unknown as MetaStreamEvent;
          if (ev.type === "done") {
            return {
              result: {
                assistant: ev.assistant ?? "",
                chat_id: ev.chat_id ?? "",
                reload_requested: ev.reload_requested ?? false,
                changed_files: ev.changed_files ?? [],
              },
            };
          }
          if (ev.type === "error") {
            throw new Error(ev.message ?? "meta-stream error");
          }
          return undefined;
        },
      }
    );
  }

  metaTranscript(sessionId: string, chatId: string): Promise<MetaMessage[]> {
    return this.client
      .post<{ messages: MetaMessage[] }>("runstatus.meta.transcript", {
        session_id: sessionId,
        chat_id: chatId,
      })
      .then((r) => r.messages ?? []);
  }

  /**
   * File/post an evidence-backed meta-improve report. The server reuses the
   * bug-report artifact pipeline so the report carries scrubbed HAR, rrweb,
   * console, and redacted trace evidence when available.
   */
  metaImproveReport(
    params: MetaImproveReportParams
  ): Promise<MetaImproveReportResult> {
    return this.client.post<MetaImproveReportResult>(
      "runstatus.meta.improve.report",
      {
        session_id: params.session_id,
        mode: params.mode,
        chat_id: params.chat_id,
        title: params.title,
        report: params.report,
        guidance: params.guidance,
        destination: params.destination,
        capture_id: params.capture_id,
        trace_ref: params.trace_ref,
        filed_by: params.filed_by,
        story_path: params.story_path,
        target_dir: params.target_dir,
        rrweb_events: params.rrweb_events,
        console_logs: params.console_logs,
        error_info: params.error_info,
      }
    );
  }

  // ── Agent-action transcripts ──────────────────────────────────────────────

  /**
   * Fetch one agent call's agent-action transcript via
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
  artifactUrl(handle: string, maxDim?: number): string {
    // Handles are content-addressed (e.g. "mockup-video#6e2b0759") and the '#'
    // is a URL fragment delimiter — left raw it truncates the path and the
    // server 404s. Encode the handle so '#' rides as %23 into the path segment.
    const base = `/artifact/${encodeURIComponent(handle)}`;
    // A downscale hint for a heavy frame rendered as a message thumbnail. The
    // server may honour it; if not, it serves full-res (the file is the same
    // URL minus the query, so caching is per-variant).
    return maxDim && maxDim > 0 ? `${base}?max=${maxDim}` : base;
  }

  /** The media handle's sibling poster still: `/artifact/<handle>/poster` — the
   *  server serves `<stem>.poster.png` beside the media keyed by the same handle.
   *  The handle is encoded as one path segment (so '#' rides as %23); the
   *  `/poster` suffix is appended after it. */
  artifactPosterUrl(handle: string): string {
    return `/artifact/${encodeURIComponent(handle)}/poster`;
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

  async videoEvents(
    sessionId: string,
    video: string
  ): Promise<{
    events: import("./session-capture.js").RrwebEvent[];
    width: number;
    height: number;
  }> {
    const res = await this.client.post<{
      events?: import("./session-capture.js").RrwebEvent[];
      width?: number;
      height?: number;
    }>("runstatus.video.events", { session_id: sessionId, video });
    return {
      events: res.events ?? [],
      width: res.width ?? 0,
      height: res.height ?? 0,
    };
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
    // The note's component-facing `anchor` is projected to the on-wire shape
    // (kind + sibling-named target) so the server decodes it the same way as the
    // offpath `anchor` param; the back-compat time_range/frame_handle stay.
    const { anchor, ...rest } = note;
    const wire = serializeAnchorParam(anchor);
    return this.client.post("runstatus.feedback.add", {
      session_id: sessionId,
      ...rest,
      ...(wire ? { anchor: wire } : {}),
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

  /** Read session-independent setup warnings for the home screen. */
  setupStatus(): Promise<SetupStatusResult> {
    return this.client.post<SetupStatusResult>("runstatus.setup.status", {});
  }

  // ── Story editor (per-story static reads; no session) ─────────────────────

  // Project object graph catalogs (W5.0's runstatus.objectgraph.load/diff)
  // moved to @kitsoki/object-graph's own kit-rpc.ts (S5,
  // .context/kits-implementation-plan.md D4) — kit.object-graph.graph.project,
  // reached via that kit's own minimal RPC client, not this class.

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

  /** Agent contracts + cassette globs for a room (runstatus.editor.agents). */
  editorAgents(
    storyPath: string,
    roomId: string
  ): Promise<EditorAgentsResult> {
    return this.client.post<EditorAgentsResult>("runstatus.editor.agents", {
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

  /** Cassette-override replay of an agent call (runstatus.editor.replay). */
  editorReplay(
    storyPath: string,
    roomId: string,
    agentIndex: number,
    cassetteFile?: string
  ): Promise<EditorReplayResult> {
    const params: Record<string, unknown> = {
      story_path: storyPath,
      room_id: roomId,
      agent_index: agentIndex,
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
  newSession(
    storyPath: string,
    opts: { initialWorld?: Record<string, unknown> } = {}
  ): Promise<string> {
    const params: Record<string, unknown> = { story_path: storyPath };
    if (opts.initialWorld && Object.keys(opts.initialWorld).length > 0) {
      params.initial_world = opts.initialWorld;
    }
    return this.client
      .post<{ session_id: string }>("runstatus.session.new", params)
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
   * Replay one recorded agent call against a chosen operator.
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
    let closed = false;
    let unsubStream: (() => void) | null = null;

    const onMessage = (raw: string) => {
      try {
        const frame = JSON.parse(raw) as {
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

    this.client
      .post<{ subscription_id: string }>("runstatus.notifications.subscribe", {})
      .then(({ subscription_id }) => {
        if (closed) return;
        subscriptionId = subscription_id;
        unsubStream = this.transport.openEventStream(
          "rpc/notifications",
          { subscription_id },
          { onMessage, onError }
        );
      })
      .catch((e) => onError?.(e));

    return () => {
      closed = true;
      unsubStream?.();
      unsubStream = null;
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
   * kitsoki, the parked agent turn blocks until the operator answers; a frame
   * lands here so the SPA can surface the modal. Same subscribe → stream →
   * backoff → unsubscribe lifecycle as the notification feed. Returns an
   * unsubscribe function.
   */
  subscribeQuestions(
    onFrame: (frame: OperatorQuestionFrame) => void,
    onError?: (e: unknown) => void
  ): () => void {
    let subscriptionId = "";
    let closed = false;
    let unsubStream: (() => void) | null = null;

    const onMessage = (raw: string) => {
      try {
        const frame = JSON.parse(raw) as {
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

    this.client
      .post<{ subscription_id: string }>("runstatus.questions.subscribe", {})
      .then(({ subscription_id }) => {
        if (closed) return;
        subscriptionId = subscription_id;
        unsubStream = this.transport.openEventStream(
          "rpc/questions",
          { subscription_id },
          { onMessage, onError }
        );
      })
      .catch((e) => onError?.(e));

    return () => {
      closed = true;
      unsubStream?.();
      unsubStream = null;
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
   * Answer a forwarded question, unblocking the parked agent turn. answers is
   * keyed by each question's text; the value is the chosen option label or a
   * custom answer (single-select) or an array of labels (multiSelect) — the
   * same shape AskUserQuestion would have returned to the agent.
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

  /**
   * File a bug report. The server attaches the held, scrubbed HAR from
   * bugPreview when a capture_id is supplied; direct callers fall back to the
   * server recorder. All params are optional — the backend defaults title/body.
   */
  reportBug(params: BugReportParams): Promise<BugReportResult> {
    return this.client.post<BugReportResult>("runstatus.bug.report", {
      capture_id: params.capture_id,
      title: params.title,
      description: params.description,
      body: params.body,
      severity: params.severity,
      repro_steps: params.repro_steps,
      trace_ref: params.trace_ref,
      filed_by: params.filed_by,
      story_path: params.story_path,
      target_dir: params.target_dir,
      screenshot_png_b64: params.screenshot_png_b64,
      rrweb_events: params.rrweb_events,
      console_logs: params.console_logs,
      error_info: params.error_info,
    });
  }

  /**
   * Read whether the current server can file bug reports. In GitHub mode this
   * is a local credential preflight; it does not probe repo-specific
   * permissions.
   */
  bugStatus(): Promise<BugStatusResult> {
    return this.client.post<BugStatusResult>("runstatus.bug.status", {});
  }

  /**
   * Take a scrubbed preview snapshot to review before filing. The browser sends
   * its observed HAR when available; the server parses, scrubs, holds, and
   * returns that exact capture for review.
   */
  bugPreview(params: BugPreviewParams = {}): Promise<BugPreviewResult> {
    return this.client.post<BugPreviewResult>("runstatus.bug.preview", {
      har_json: params.har_json,
    });
  }

  /** The last failed RPC (for bug-report error context), or null. */
  lastRpcError(): LastRpcError | null {
    return this.client.getLastError();
  }
}

/** Request shape for runstatus.bug.preview. */
export interface BugPreviewParams {
  /** JSON.stringify(HAR 1.2) from the browser-observed network recorder. */
  har_json?: string;
}

/** Request shape for runstatus.bug.report — all fields optional. */
export interface BugReportParams {
  /** id from a prior bug.preview; files the EXACT held scrubbed HAR. */
  capture_id?: string;
  title?: string;
  /** operator prose — becomes the bug body (preferred over legacy `body`). */
  description?: string;
  body?: string;
  severity?: string;
  repro_steps?: string[];
  trace_ref?: string;
  filed_by?: string;
  story_path?: string;
  target_dir?: string;
  /** base64 PNG (no data: prefix). */
  screenshot_png_b64?: string;
  /** JSON string (or base64-of-JSON) of the rrweb event array. */
  rrweb_events?: string;
  /** JSON string: array of {level, ts, text}. */
  console_logs?: string;
  /** JSON string: {errors:[...], last_rpc:{method,code,message}}. */
  error_info?: string;
}

/** A HAR header entry. */
export interface HarHeader {
  name: string;
  value: string;
}

/** One HAR request/response exchange. */
export interface HarEntry {
  startedDateTime?: string;
  time?: number;
  comment?: string;
  request?: {
    method?: string;
    url?: string;
    headers?: HarHeader[];
    queryString?: HarHeader[];
    postData?: { mimeType?: string; text?: string };
  };
  response?: {
    status?: number;
    headers?: HarHeader[];
    content?: { size?: number; mimeType?: string; text?: string };
  };
}

/** HAR 1.2 document (the scrubbed shape returned by bug.preview). */
export interface Har {
  log: {
    version?: string;
    creator?: { name?: string; version?: string };
    entries: HarEntry[];
  };
}

/** Result of runstatus.bug.preview — a held, scrubbed capture to review. */
export interface BugPreviewResult {
  /** pass back to bug.report to file this exact scrubbed HAR. */
  capture_id: string;
  /** scrubbed HAR 1.2 document. */
  har: Har;
  /** # of network exchanges retained. */
  depth: number;
  /** ring-buffer capacity. */
  capacity: number;
}

/** Request shape for runstatus.meta.improve.report. */
export interface MetaImproveReportParams {
  session_id: string;
  mode?: string;
  chat_id?: string;
  title?: string;
  report?: string;
  guidance?: string;
  /** configured = provider/GitHub/local according to server flags; local forces .artifacts. */
  destination?: "configured" | "local" | "ticket-provider";
  capture_id?: string;
  trace_ref?: string;
  filed_by?: string;
  story_path?: string;
  target_dir?: string;
  rrweb_events?: string;
  console_logs?: string;
  error_info?: string;
}

/** Result of runstatus.meta.improve.report. */
export interface MetaImproveReportResult {
  id?: string | number;
  path?: string;
  url?: string;
  sink?: "local-artifact" | "github" | "ticket-provider" | string;
  report_kind?: "meta-improve" | string;
  destination?: string;
  artifacts?: string[];
  artifacts_path?: string;
  local_path?: string;
  local_artifacts_path?: string;
  provider?: string;
  provider_ok?: boolean;
  provider_error?: string;
  provider_hint?: string;
}

/** Result of runstatus.bug.status. */
export interface BugStatusResult {
  mode: "github" | "local" | "local-artifact";
  repo?: string;
  can_file: boolean;
  github_auth_configured?: boolean;
  warning?: string;
  setup_hint?: string;
}

/** Result of runstatus.setup.status. */
export interface SetupStatusResult {
  warnings: SetupWarning[];
  project_onboarded?: boolean;
}

export interface SetupWarning {
  id: string;
  title: string;
  body: string;
  action_label?: string;
  action_command?: string;
  story_id?: string;
  story_ref?: string;
}

/** Result of runstatus.bug.report. */
export interface BugReportResult {
  /** bare filename without .md, e.g. "2026-06-12T130405Z-foo". */
  id: string;
  /** filed path, e.g. ".artifacts/issues/bugs/<id>.md". */
  path?: string;
  /** GitHub issue URL when the server is in GitHub filing mode. */
  url?: string;
  /** True when the report was filed as a GitHub issue. */
  github?: boolean;
  /** Privacy gate outcome returned by the server. */
  privacy?: BugPrivacyResult;
}

export interface BugPrivacyResult {
  status: string;
  message?: string;
  follow_up_path?: string;
}

// Re-export for components that import AnnotationEntry from this module.
export type { AnnotationEntry };

/**
 * visualParams maps a VisualBundle into the exact wire shape the server's
 * `runstatus.session.offpath` `visual` param expects (mirrors host.VisualAmbient
 * JSON tags). The only impedance is the bbox: the resolver records it as
 * {x,y,width,height} (readable in the chip + tests), but host.VisualAmbient's
 * `element.bbox` is a positional `[x, y, w, h]` array — flatten it here. Fields
 * the bundle omits are simply absent; the server decodes missing fields to zero
 * and `WithVisualAmbient` is a no-op when nothing meaningful was attached.
 */
function visualParams(
  v: import("./source.js").VisualBundle
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  if (v.frame_handle) out.frame_handle = v.frame_handle;
  if (v.media_handle) out.media_handle = v.media_handle;
  if (v.route) out.route = v.route;
  if (typeof v.t_ms === "number") out.t_ms = v.t_ms;
  if (v.point) out.point = { x: v.point.x, y: v.point.y };
  if (v.element) {
    const { selector, role, text, bbox } = v.element;
    out.element = {
      selector,
      role,
      text,
      bbox: [bbox.x, bbox.y, bbox.width, bbox.height],
    };
  }
  return out;
}

/**
 * serializeAnchorParam projects the component AnnotationAnchor into the on-wire
 * AnchorWire object host.AnchorFromParams decodes (kind + sibling-named target).
 * Returns undefined when there is no anchor or no target (the server then
 * synthesizes one from the flat visual fields). UI-only fields (point, label,
 * id) are dropped by serializeAnchor.
 */
function serializeAnchorParam(
  anchor?: import("../lib/annotationAnchor.js").AnnotationAnchor
): import("../lib/annotationAnchor.js").AnchorWire | undefined {
  if (!anchor) return undefined;
  return serializeAnchor(anchor) ?? undefined;
}
