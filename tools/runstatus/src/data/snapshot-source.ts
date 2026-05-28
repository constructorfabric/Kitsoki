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
    const s = raw as Snapshot;
    this.snap = { ...s, events: repairOracleTurns(s.events) };
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

/**
 * Oracle journal entries written during RunInitialOnEnter carry turn=0 even
 * though the oracle calls logically belong to the turns that follow. Fix this
 * by reassigning each oracle event's turn to the highest turn seen in the
 * non-oracle store events whose time is <= the oracle entry's completion time.
 *
 * We use the oracle event's own time (which is the completion timestamp for
 * complete events, or the back-calculated start for start events) to look up
 * the nearest preceding turn. For start events recorded from an earlier run
 * the start timestamp may predate all store events; in that case we use the
 * paired complete event's completion timestamp instead.
 */
function repairOracleTurns(events: TraceEvent[]): TraceEvent[] {
  const isOracle = (e: TraceEvent) => e.msg.startsWith("oracle.");

  // Build a sorted list of (time, turn) from non-oracle events for lookup.
  const anchors = events
    .filter((e) => !isOracle(e) && e.turn > 0)
    .map((e) => ({ time: e.time, turn: e.turn }))
    .sort((a, b) => a.time < b.time ? -1 : a.time > b.time ? 1 : 0);

  if (anchors.length === 0) return events;

  // Build a call_id → complete-event time map so start events can borrow
  // the completion timestamp when their own timestamp precedes all anchors.
  const completeTimeByCallId = new Map<string, string>();
  for (const e of events) {
    if (e.msg.match(/^oracle\.\w+\.complete$/) && typeof e.attrs?.call_id === "string") {
      completeTimeByCallId.set(e.attrs.call_id as string, e.time);
    }
  }

  function nearestTurn(iso: string): number {
    let turn = 0;
    for (const a of anchors) {
      if (a.time > iso) break;
      if (a.turn > turn) turn = a.turn;
    }
    // Fallback: if no preceding anchor has a turn > 0, use the first anchor
    // that comes after iso. This handles oracle calls that complete within a
    // millisecond before the first turn.start event.
    if (turn === 0) {
      for (const a of anchors) {
        if (a.time >= iso) { turn = a.turn; break; }
      }
    }
    return turn;
  }

  return events.map((e) => {
    if (!isOracle(e) || e.turn !== 0) return e;

    let lookupTime = e.time;
    // For start events whose timestamp predates the session, borrow the
    // paired complete event's timestamp.
    if (e.msg.match(/^oracle\.\w+\.start$/) && typeof e.attrs?.call_id === "string") {
      const completeTime = completeTimeByCallId.get(e.attrs.call_id as string);
      if (completeTime && completeTime > lookupTime) lookupTime = completeTime;
    }

    const repaired = nearestTurn(lookupTime);
    return repaired > 0 ? { ...e, turn: repaired } : e;
  });
}
