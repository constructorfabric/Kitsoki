export interface NodeRef {
  kind: "state" | "effect" | "transition" | "world";
  ref: string;
}

export interface SessionHeader {
  session_id: string;
  app_id: string;
  current_state: string;
  turn: number;
  started_at: string; // ISO 8601
  terminal: boolean;
}

export interface MermaidSnapshot {
  source: string;
  node_map: Record<string, NodeRef>;
}

export interface TraceEvent {
  time: string;
  level: string;
  msg: string;
  session_id: string;
  turn: number;
  state_path: string;
  /** Non-zero for off-path event batches: the foreground turn that was active
   *  when the off-path interaction occurred. The trace UI uses this to render
   *  off-path groups as nested sub-items rather than independent sibling turns. */
  parent_turn?: number;
  attrs: Record<string, unknown>;
}

// Keep AppDef loose; components destructure what they need.
export interface AppDef {
  id: string;
  name?: string;
  root: string;
  states: Record<string, unknown>;
  [key: string]: unknown;
}

export interface Snapshot {
  session: SessionHeader;
  app: AppDef;
  mermaid: MermaidSnapshot;
  events: TraceEvent[];
}
