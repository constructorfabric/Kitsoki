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
import type { TranscriptData, TranscriptEvent } from "./transcript.js";
import { semanticSidecarName } from "../lib/semanticPlugins.js";

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
    // The Go trace is canonical: agent events are stamped with their real
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

  // ── Active-session discovery ─────────────────────────────────────────────
  // A snapshot IS the single session, so the current session is its session id.

  getCurrentSession(): Promise<string | null> {
    return Promise.resolve(this.snap.session.session_id ?? null);
  }

  /** Invoke onChange once with the snapshot session id; return a no-op disposer. */
  subscribeCurrentSession(
    onChange: (sessionId: string | null) => void
  ): () => void {
    onChange(this.snap.session.session_id ?? null);
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
    _slots?: Record<string, unknown>,
    _anchor?: import("../lib/annotationAnchor.js").AnnotationAnchor
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

  offpath(
    _sessionId: string,
    _input: string,
    _visual?: import("./source.js").VisualBundle,
    _anchor?: import("../lib/annotationAnchor.js").AnnotationAnchor
  ): Promise<{ answer: string }> {
    return this.readOnly("offpath");
  }

  /**
   * Read the artifact's semantic sidecar from the static export: a snapshot
   * ships its artifacts under `./artifacts/<handle>`, so the sidecar is at
   * `./artifacts/<name>.semantic.json` (the handle's basename + `.semantic.json`).
   * Resolves null when there is no sidecar (a plain image/video) so the
   * annotator falls back to the dom_node picker. Never throws — a missing
   * sidecar is the common, non-error case offline.
   */
  async semanticMap(
    _sessionId: string,
    handle: string
  ): Promise<import("../lib/semanticPlugins.js").SemanticSidecar | null> {
    const sidecar = `./artifacts/${semanticSidecarName(handle)}`;
    try {
      const res = await fetch(sidecar);
      if (!res.ok) return null;
      const env = (await res.json()) as import("../lib/semanticPlugins.js").SemanticSidecar;
      if (!env || !env.elements || env.elements.length === 0) return null;
      return env;
    } catch {
      return null;
    }
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

  // ── Agent-action transcripts ──────────────────────────────────────────────

  /**
   * Resolve one agent call's agent-action transcript from the INLINED snapshot
   * data — NOT a readOnly() throw, because a static export must still show the
   * drawer with no server. artifact.go's inlineTranscriptSidecars folded each
   * event's sidecar into attrs.transcript = {format, events, timings,
   * schema_version}; we find the event carrying this call_id and return its
   * inlined transcript. A call with no inlined transcript resolves to an empty
   * TranscriptData (no drawer body), matching the live server's no-sidecar case.
   */
  getTranscript(_sessionId: string, callId: string): Promise<TranscriptData> {
    const empty: TranscriptData = {
      format: "claude-stream-json",
      events: [],
      timings: [],
      schemaVersion: 1,
    };
    for (const ev of this.snap.events) {
      const attrs = ev.attrs as Record<string, unknown> | undefined;
      if (!attrs || attrs["call_id"] !== callId) continue;
      const t = attrs["transcript"] as
        | {
            format?: string;
            events?: TranscriptEvent[];
            timings?: number[];
            schema_version?: number;
          }
        | undefined;
      if (!t) return Promise.resolve(empty);
      return Promise.resolve({
        format: t.format ?? "claude-stream-json",
        events: t.events ?? [],
        timings: t.timings ?? [],
        schemaVersion: t.schema_version ?? 1,
      });
    }
    return Promise.resolve(empty);
  }

  // ── Media artifacts ───────────────────────────────────────────────────────

  /**
   * Returns a relative sidecar path for offline/snapshot mode. The snapshot
   * HTML artifact is expected to live alongside an `artifacts/` directory
   * containing the media files keyed by handle (filename).
   */
  artifactUrl(handle: string, _maxDim?: number): string {
    // A static snapshot ships its artifacts verbatim under artifacts/<handle>;
    // there is no server to downscale, so the maxDim hint is ignored.
    return `./artifacts/${handle}`;
  }

  /** The sibling poster still for a media handle: `<stem>.poster.png` under
   *  artifacts/. A content-addressed handle (`deck#abc` / `deck.mp4`) maps to
   *  `deck.poster.png`, mirroring host.PosterSidecarPath. */
  artifactPosterUrl(handle: string): string {
    const base = handle.split("/").pop() ?? handle;
    const stem = base.split("#")[0].replace(/\.[^.]+$/, "");
    return `./artifacts/${stem}.poster.png`;
  }

  // ── Video feedback mode (/review) ──────────────────────────────────────────
  // A static snapshot is read-only and has no server to grab stills or persist
  // notes: chapters are unavailable (the sidecar is not bundled) and the
  // capture/dispatch RPCs reject — the panel degrades to "no chapters" and
  // disables flagging. The live surface is where /review is used.

  videoChapters(): Promise<import("./source.js").Chapter[]> {
    return Promise.resolve([]);
  }

  videoFrame(): Promise<{ handle: string; mime: string; kind: string }> {
    return Promise.reject(
      new Error("video.frame is unavailable in snapshot mode")
    );
  }

  addFeedback(): Promise<{ ok: boolean }> {
    return Promise.reject(
      new Error("feedback.add is unavailable in snapshot mode")
    );
  }
}
