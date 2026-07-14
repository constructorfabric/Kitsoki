/**
 * Project object graph wire types — mirror internal/app/graph/wire.go's
 * KitsokiGraph and the runstatus.objectgraph.* RPC family
 * (internal/runstatus/server/objectgraph.go). Same shape story room graphs
 * use (runstatus.editor.graph) — W5.0 keeps one wire shape so one renderer
 * (GraphCanvas, ../components/objectgraph/) draws both.
 */

export interface ObjectGraphRef {
  kind: string;
  ref: string;
}

export interface ObjectGraphNode {
  id: string;
  kind: string;
  label: string;
  ref: ObjectGraphRef;
  group?: string;
  status?: string;
  attrs?: Record<string, unknown>;
}

export interface ObjectGraphEdge {
  id: string;
  kind: string;
  source: string;
  target: string;
  label?: string;
  status?: string;
  attrs?: Record<string, unknown>;
}

export interface ObjectGraph {
  schema: string;
  graph_id: string;
  kind: string;
  directed: boolean;
  cyclic: boolean;
  nodes: ObjectGraphNode[];
  edges: ObjectGraphEdge[];
}
