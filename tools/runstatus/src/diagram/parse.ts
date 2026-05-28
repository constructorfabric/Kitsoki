/**
 * Parse the mermaid source string emitted by `kitsoki viz --flowchart` into a
 * structured model we can render ourselves.
 *
 * We deliberately do NOT trust the "phase N · NAME" numbers in the source —
 * stories author them by yaml-key order, which doesn't always match visit
 * order (bugfix has validating=2 but it runs near the end). Instead we
 * topologically order phases by their min-distance from `Start` along
 * forward edges, with first-appearance order as the tiebreaker.
 */

export interface Phase {
  /** Internal id, e.g. "SG_idle". */
  id: string;
  /** Display name, e.g. "idle" (the part after "phase N · "). */
  name: string;
  /** Optional one-line description after " — ". */
  desc: string;
  /** Original "phase N" if present, else null. */
  phaseNumber: number | null;
  /** Rooms in this phase, ordered. */
  rooms: Room[];
}

export interface Room {
  /** Internal id, e.g. "ST_idle". */
  id: string;
  /** Display label, e.g. "idle". */
  label: string;
  /** Owning phase id. */
  phaseId: string;
  /** Distance from Start along forward edges (Infinity if unreachable). */
  distance: number;
}

export interface Edge {
  from: string;
  to: string;
  /** Intent / condition label. Empty string for plain "-->" arrows. */
  label: string;
  /** True for self-loops (from == to). */
  selfLoop: boolean;
}

export interface Diagram {
  phases: Phase[];
  edges: Edge[];
  /** The Start pseudo-node id, usually "Start". */
  startId: string | null;
  /** The first real room reached from Start. */
  startRoomId: string | null;
  /** roomId → phaseId. */
  phaseByRoom: Map<string, string>;
  /** roomId → Room. */
  roomById: Map<string, Room>;
}

// ---------------------------------------------------------------------------
// Parsing
// ---------------------------------------------------------------------------

// Mermaid identifiers are word chars only (letters, digits, underscore).
const IDENT = "[A-Za-z_][A-Za-z0-9_]*";

const SUBGRAPH_RE = new RegExp(`^\\s*subgraph\\s+(${IDENT})\\["?(.+?)"?\\]\\s*$`);
const ROOM_DECL_RE = new RegExp(`^\\s*(${IDENT})[[\\(\\/].*$`);
const EDGE_LABELED_RE = new RegExp(`^\\s*(${IDENT})\\s*--\\s*"(.+)"\\s*-->\\s*(${IDENT})\\s*$`);
const EDGE_PIPED_RE = new RegExp(`^\\s*(${IDENT})\\s*-->\\s*\\|(.+)\\|\\s*(${IDENT})\\s*$`);
const EDGE_PLAIN_RE = new RegExp(`^\\s*(${IDENT})\\s*-->\\s*(${IDENT})\\s*$`);

/**
 * Strip leading "<b>...</b>" tags from a subgraph label and split into
 * (name, desc) by the " — " separator that viz emits.
 *
 * Examples:
 *   "<b>phase 0 · idle</b> — Bug-fix pipeline parked. Waiting for start."
 *     → { phaseNumber: 0, name: "idle", desc: "Bug-fix pipeline parked. …" }
 *   "<b>__exit__done</b> — Exit: done"
 *     → { phaseNumber: null, name: "__exit__done", desc: "Exit: done" }
 */
function parsePhaseLabel(raw: string): {
  phaseNumber: number | null;
  name: string;
  desc: string;
} {
  // Strip <b>...</b>
  const stripped = raw.replace(/<b>(.*?)<\/b>/g, "$1");
  const dashIdx = stripped.indexOf(" — ");
  const head = dashIdx >= 0 ? stripped.slice(0, dashIdx).trim() : stripped.trim();
  const desc = dashIdx >= 0 ? stripped.slice(dashIdx + 3).trim() : "";

  // "phase N · NAME" form
  const m = head.match(/^phase\s+(\d+)\s*[·•\-]\s*(.+)$/);
  if (m) {
    return { phaseNumber: Number(m[1]), name: m[2]!.trim(), desc };
  }
  return { phaseNumber: null, name: head, desc };
}

/**
 * Strip mermaid shape decorators from a node label.
 *
 * Examples:
 *   `[/"idle"/]:::room`        → "idle"
 *   `(["Start"]):::input`      → "Start"
 *   `["foo"]`                  → "foo"
 */
function parseRoomLabel(rest: string): string {
  // Match the first quoted content.
  const m = rest.match(/"([^"]*)"/);
  return m ? m[1]! : rest.trim();
}

export function parseDiagram(source: string): Diagram {
  const lines = source.split(/\r?\n/);

  const phases: Phase[] = [];
  const phaseById = new Map<string, Phase>();
  const roomById = new Map<string, Room>();
  const phaseByRoom = new Map<string, string>();
  const edges: Edge[] = [];

  let currentPhase: Phase | null = null;
  let startId: string | null = null;

  for (const rawLine of lines) {
    const line = rawLine.replace(/%%.*$/, ""); // strip trailing comments
    if (!line.trim()) continue;
    if (line.trim().startsWith("%%")) continue;
    if (/^\s*classDef\s/.test(line)) continue;
    if (/^\s*direction\s/.test(line)) continue;
    if (/^\s*flowchart\s/.test(line)) continue;

    // Subgraph open
    const sg = line.match(SUBGRAPH_RE);
    if (sg) {
      const id = sg[1]!;
      const { phaseNumber, name, desc } = parsePhaseLabel(sg[2]!);
      const p: Phase = { id, name, desc, phaseNumber, rooms: [] };
      phases.push(p);
      phaseById.set(id, p);
      currentPhase = p;
      continue;
    }
    if (/^\s*end\s*$/.test(line)) {
      currentPhase = null;
      continue;
    }

    // Detect Start node — `Start(["..."]):::input`
    const startM = line.match(/^\s*(Start)\(\[".*"\]\)/);
    if (startM) {
      startId = startM[1]!;
      continue;
    }

    // Labelled edge: ST_a -- "label" --> ST_b
    const eL = line.match(EDGE_LABELED_RE);
    if (eL) {
      edges.push({ from: eL[1]!, to: eL[3]!, label: eL[2]!, selfLoop: eL[1] === eL[3] });
      continue;
    }
    // Piped edge: ST_a -->|label| ST_b
    const eP = line.match(EDGE_PIPED_RE);
    if (eP) {
      edges.push({ from: eP[1]!, to: eP[3]!, label: eP[2]!, selfLoop: eP[1] === eP[3] });
      continue;
    }
    // Plain edge: Start --> ST_idle  (label-less)
    const ePl = line.match(EDGE_PLAIN_RE);
    if (ePl) {
      edges.push({ from: ePl[1]!, to: ePl[2]!, label: "", selfLoop: ePl[1] === ePl[2] });
      continue;
    }

    // Room declaration (only meaningful inside a subgraph)
    const rd = line.match(ROOM_DECL_RE);
    if (rd && currentPhase) {
      const id = rd[1]!;
      // Take everything after the id as the rest, extract the quoted label.
      const rest = line.trim().slice(id.length);
      const label = parseRoomLabel(rest);
      const room: Room = {
        id,
        label,
        phaseId: currentPhase.id,
        distance: Infinity,
      };
      currentPhase.rooms.push(room);
      roomById.set(id, room);
      phaseByRoom.set(id, currentPhase.id);
      continue;
    }
  }

  // ---- Distance from Start via BFS ----------------------------------------
  // Some edges target a "ghost" id that refers to a phase rather than a real
  // room — e.g. `ST_foyer -- "go" --> ST_bar` where `ST_bar` is not declared
  // anywhere but `SG_bar` is a phase with rooms ST_bar_dark / ST_bar_lit.
  // (kitsoki viz emits this for compound-state targets.)  We expand any such
  // edge to all rooms in the matching phase during BFS so distances propagate.
  const phaseByStateName = new Map<string, Phase>(); // "bar" → SG_bar
  for (const p of phases) {
    // Phase id is "SG_<name>"; the ghost room id would be "ST_<name>".
    const m = p.id.match(/^SG_(.+)$/);
    if (m) phaseByStateName.set(m[1]!, p);
  }
  function expandTarget(targetId: string): string[] {
    if (roomById.has(targetId)) return [targetId];
    const m = targetId.match(/^ST_(.+)$/);
    if (m) {
      const ph = phaseByStateName.get(m[1]!);
      if (ph) return ph.rooms.map((r) => r.id);
    }
    return [];
  }

  // Build forward adjacency for rooms only (skip self-loops for distance).
  // We compute TWO adjacencies:
  //   `adjAll`        — every non-self-loop edge (reachability)
  //   `adjForward`    — only edges whose label marks a "promotion" arc
  //                     (empty, accept, start, proceed).  This skips
  //                     refine / restart_from / jump_to / quit, which would
  //                     otherwise flatten the topo order — e.g. bugfix's
  //                     `jump_to` arcs let validating appear distance-2 from
  //                     reproducing, which is correct reachability but a lie
  //                     about flow.
  const adjAll = new Map<string, string[]>();
  const adjForward = new Map<string, string[]>();
  const FORWARD_LABELS = new Set(["", "accept", "start", "proceed"]);
  for (const e of edges) {
    if (e.selfLoop) continue;
    const arr = adjAll.get(e.from) ?? [];
    for (const t of expandTarget(e.to)) arr.push(t);
    adjAll.set(e.from, arr);

    if (FORWARD_LABELS.has(e.label)) {
      const arrF = adjForward.get(e.from) ?? [];
      for (const t of expandTarget(e.to)) arrF.push(t);
      adjForward.set(e.from, arrF);
    }
  }
  const adj = adjAll; // used by BFS below

  // Find the first real room from Start.  We have to scan `edges` (not `adj`)
  // because adj only carries room-resolved targets; `Start` itself may map
  // through a ghost id.
  let startRoomId: string | null = null;
  if (startId) {
    for (const e of edges) {
      if (e.from !== startId) continue;
      const resolved = expandTarget(e.to);
      if (resolved.length > 0) {
        startRoomId = resolved[0]!;
        break;
      }
    }
  }

  // BFS from startRoomId; if no Start, BFS from the first declared room.
  const bfsRoot = startRoomId ?? phases[0]?.rooms[0]?.id ?? null;
  // forwardDist is the "promotion-only" distance — used for phase ordering.
  // distance (room.distance) stays the reachability distance.
  const forwardDist = new Map<string, number>();
  if (bfsRoot) {
    runBfs(adj, bfsRoot, (id, d) => {
      const r = roomById.get(id);
      if (r) r.distance = d;
    });
    runBfs(adjForward, bfsRoot, (id, d) => {
      forwardDist.set(id, d);
    });
  }

  function runBfs(
    g: Map<string, string[]>,
    root: string,
    visit: (id: string, dist: number) => void,
  ): void {
    const dist = new Map<string, number>([[root, 0]]);
    const queue: string[] = [root];
    visit(root, 0);
    while (queue.length > 0) {
      const cur = queue.shift()!;
      const d = dist.get(cur)!;
      for (const next of g.get(cur) ?? []) {
        if (dist.has(next)) continue;
        dist.set(next, d + 1);
        visit(next, d + 1);
        queue.push(next);
      }
    }
  }

  // ---- Order phases by min-distance --------------------------------------
  // Tiebreakers: declared-phaseNumber (if any), then first-appearance index.
  const declaredOrder = new Map<string, number>();
  phases.forEach((p, i) => declaredOrder.set(p.id, i));

  function phaseMinForwardDistance(p: Phase): number {
    let m = Infinity;
    for (const r of p.rooms) {
      const d = forwardDist.get(r.id) ?? Infinity;
      if (d < m) m = d;
    }
    return m;
  }
  function phaseMinDistance(p: Phase): number {
    let m = Infinity;
    for (const r of p.rooms) if (r.distance < m) m = r.distance;
    return m;
  }
  function isExitPhase(p: Phase): boolean {
    // Convention: exit pseudo-phases author-named "__exit__<name>" (and the
    // legacy cloak "ended" terminal).  Push them to the end so the main
    // flow reads top-to-bottom cleanly.
    return p.name.startsWith("__exit__");
  }

  phases.sort((a, b) => {
    // 1. Exits always last.
    const ae = isExitPhase(a) ? 1 : 0;
    const be = isExitPhase(b) ? 1 : 0;
    if (ae !== be) return ae - be;
    // 2. Promotion-only distance — places phases along the canonical flow
    //    even when there are many cross-phase shortcut arcs.
    const fa = phaseMinForwardDistance(a);
    const fb = phaseMinForwardDistance(b);
    if (fa !== fb) return fa - fb;
    // 3. Declared phase number (when present and meaningful).
    const pa = a.phaseNumber ?? Number.POSITIVE_INFINITY;
    const pb = b.phaseNumber ?? Number.POSITIVE_INFINITY;
    if (pa !== pb) return pa - pb;
    // 4. Reachability distance (any-edge).
    const da = phaseMinDistance(a);
    const db = phaseMinDistance(b);
    if (da !== db) return da - db;
    // 5. Source declaration order.
    return (declaredOrder.get(a.id) ?? 0) - (declaredOrder.get(b.id) ?? 0);
  });

  // Sort rooms within each phase by distance, then by declaration index.
  for (const p of phases) {
    p.rooms.sort((a, b) => {
      if (a.distance !== b.distance) return a.distance - b.distance;
      return a.id.localeCompare(b.id);
    });
  }

  return {
    phases,
    edges,
    startId,
    startRoomId,
    phaseByRoom,
    roomById,
  };
}
