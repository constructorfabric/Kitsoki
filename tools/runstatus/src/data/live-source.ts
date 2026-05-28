import type {
  SessionHeader,
  AppDef,
  MermaidSnapshot,
  TraceEvent,
} from "../types.js";
import type { DataSource, TraceCursor } from "./source.js";
import { JsonRpcClient } from "../transport/jsonrpc.js";

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
}
