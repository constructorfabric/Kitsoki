import type {
  SessionHeader,
  AppDef,
  MermaidSnapshot,
  TraceEvent,
  Snapshot,
} from "../types.js";
import type { DataSource, TraceCursor } from "./source.js";

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
}
