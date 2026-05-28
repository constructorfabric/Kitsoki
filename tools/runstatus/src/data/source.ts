import type {
  SessionHeader,
  AppDef,
  MermaidSnapshot,
  TraceEvent,
} from "../types.js";
import { SnapshotSource } from "./snapshot-source.js";
import { LiveSource } from "./live-source.js";

export interface TraceCursor {
  since_turn?: number;
  until_turn?: number;
  limit?: number;
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
