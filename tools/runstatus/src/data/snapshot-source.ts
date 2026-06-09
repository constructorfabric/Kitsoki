import type {
  SessionHeader,
  AppDef,
  MermaidSnapshot,
  TraceEvent,
  Snapshot,
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

/**
 * DataSource backed by an embedded Snapshot blob (artifact / offline mode).
 * All methods resolve synchronously from the in-memory snapshot.
 */
export class SnapshotSource implements DataSource {
  private readonly snap: Snapshot;

  constructor(snap?: unknown) {
    const win = (
      typeof window !== "undefined"
        ? window
        : {}
    ) as { __KITSOKI_SNAPSHOT__?: unknown };
    const raw = snap ?? win.__KITSOKI_SNAPSHOT__;
    if (raw === undefined) {
      throw new Error(
        "SnapshotSource: no snapshot provided and window.__KITSOKI_SNAPSHOT__ is undefined"
      );
    }
    // The Go trace is canonical: oracle events are stamped with their real
    // foreground turn upstream, so the snapshot equals the on-disk trace and
    // needs no load-time mutation.
    this.snap = raw as Snapshot;
  }

  listSessions(): Promise<SessionHeader[]> {
    return Promise.resolve([this.snap.session]);
  }

  getSession(_sessionId: string): Promise<SessionHeader> {
    return Promise.resolve(this.snap.session);
  }

  getApp(_sessionId: string): Promise<AppDef> {
    return Promise.resolve(this.snap.app);
  }

  getMermaid(_sessionId: string, _detail?: string): Promise<MermaidSnapshot> {
    return Promise.resolve(this.snap.mermaid);
  }

  getTrace(
    _sessionId: string,
    cursor?: TraceCursor
  ): Promise<{ events: TraceEvent[]; last_turn: number }> {
    let events = this.snap.events.slice();

    if (cursor?.since_turn !== undefined) {
      events = events.filter((e) => e.turn >= (cursor.since_turn ?? 0));
    }
    if (cursor?.until_turn !== undefined) {
      events = events.filter((e) => e.turn <= (cursor.until_turn ?? Infinity));
    }
    if (cursor?.limit !== undefined && cursor.limit > 0) {
      events = events.slice(0, cursor.limit);
    }

    const last_turn =
      events.length > 0
        ? Math.max(...events.map((e) => e.turn))
        : (this.snap.session.turn ?? 0);

    return Promise.resolve({ events, last_turn });
  }

  /** No-op: snapshot data is static. */
  subscribe(_sessionId: string, _onEvent: (e: TraceEvent) => void): () => void {
    return () => undefined;
  }

  // ── Write/read RPCs ────────────────────────────────────────────────────
  //
  // A snapshot is a frozen, read-only trace artifact: there is no live session
  // to advance or query. Every write/read RPC rejects so callers fail loudly
  // rather than silently no-op against stale data.

  private readOnly(method: string): never {
    throw new Error(
      `SnapshotSource: ${method} is unavailable — a snapshot is a read-only artifact (no live session)`
    );
  }

  view(_sessionId: string): Promise<TurnResult> {
    return this.readOnly("view");
  }

  submit(
    _sessionId: string,
    _intent: string,
    _slots?: Record<string, unknown>
  ): Promise<TurnResult> {
    return this.readOnly("submit");
  }

  sendTurn(_sessionId: string, _input: string): Promise<TurnResult> {
    return this.readOnly("sendTurn");
  }

  continueTurn(
    _sessionId: string,
    _slots: Record<string, unknown>
  ): Promise<TurnResult> {
    return this.readOnly("continueTurn");
  }

  offpath(_sessionId: string, _input: string): Promise<{ answer: string }> {
    return this.readOnly("offpath");
  }

  // ── Meta mode ────────────────────────────────────────────────────────────
  // A snapshot has no live engine behind it, so meta mode is unavailable; the
  // global meta button hides itself in snapshot mode (it checks isSnapshot()).

  metaModes(_sessionId: string): Promise<MetaModeInfo[]> {
    // Soft-fail: an empty list lets the button render disabled rather than
    // throwing during a passive home-screen poll.
    return Promise.resolve([]);
  }

  metaEnter(
    _sessionId: string,
    _mode: string,
    _chatId?: string
  ): Promise<MetaSession> {
    return this.readOnly("metaEnter");
  }

  metaSend(
    _sessionId: string,
    _mode: string,
    _chatId: string,
    _input: string
  ): Promise<MetaSendResult> {
    return this.readOnly("metaSend");
  }

  metaNew(
    _sessionId: string,
    _mode: string,
    _chatId: string
  ): Promise<MetaSession> {
    return this.readOnly("metaNew");
  }

  metaTranscript(_sessionId: string, _chatId: string): Promise<MetaMessage[]> {
    return this.readOnly("metaTranscript");
  }

  // ── Media artifacts ───────────────────────────────────────────────────────

  /**
   * Returns a relative sidecar path for offline/snapshot mode. The snapshot
   * HTML artifact is expected to live alongside an `artifacts/` directory
   * containing the media files keyed by handle (filename).
   */
  artifactUrl(handle: string): string {
    return `./artifacts/${handle}`;
  }
}
