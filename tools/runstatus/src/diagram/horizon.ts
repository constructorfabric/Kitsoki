/**
 * Pure derivations for the state diagram's PATH + HORIZON ("metro stepper")
 * render mode. See docs/tracing/run-status-ui.md → "Path & horizon mode".
 *
 * These are PURE functions over the parsed `Diagram` (src/diagram/parse.ts) plus
 * plain event / intent data — NO Vue / Pinia / DOM imports — so they can be
 * imported by both the Vitest unit suite and the Node-based Playwright spec, and
 * unit-tested in isolation.
 *
 * The render has three truth tiers and the design hinges on NOT blurring them
 * (tools/runstatus/CLAUDE.md: never let the view imply something the trace
 * doesn't). Each function below is tagged with its tier:
 *
 *   - trace      — ground truth: the run actually went there.
 *   - live       — ground truth: what you can do right now.
 *   - projection — what the static graph DECLARES, not what ran.
 */

import type { Diagram, Phase, Room, Edge } from "./parse.js";

/** A plain intent descriptor — the fields of types.ts IntentInfo this module
 *  needs. Kept structural (not an import of types.ts) so the module stays a
 *  leaf with no app-type coupling. */
export interface HorizonIntent {
  name: string;
  title?: string;
}

/**
 * Longest-prefix match of a landed `state_path` to a room id. Mirrors the
 * matcher StateDiagram.vue used inline (currentRoomId): a path matches a room
 * when it equals the room label or descends from it (`label + "."` prefix);
 * when several rooms match, the LONGEST label wins (so "reproducing._executing"
 * resolves to "reproducing", and "leg_a_executing.traveling" to
 * "leg_a_executing", never a shorter shadowing label).
 *
 * Exported so the component and the tests share ONE matcher.
 *
 * Source / truth status: n/a (pure structural lookup, used by all tiers).
 */
export function matchRoomId(statePath: string, diagram: Diagram | null): string | null {
  if (!diagram || !statePath) return null;
  let best: string | null = null;
  let bestLen = -1;
  for (const room of diagram.roomById.values()) {
    const lbl = room.label;
    if (statePath === lbl || statePath.startsWith(lbl + ".")) {
      if (lbl.length > bestLen) {
        best = room.id;
        bestLen = lbl.length;
      }
    }
  }
  return best;
}

/** The fields of a trace event traveledPath reads. Structural, not a types.ts
 *  import, to keep this module a leaf. */
export interface StateEnteredish {
  msg: string;
  state_path: string;
}

/**
 * The ordered room ids the run actually visited, derived from the
 * `machine.state_entered` event stream. Each `state_entered` carries the LANDED
 * (TO) state, so it is authoritative (see run.ts applyStatePath). We map each
 * landed path to a room via matchRoomId, drop nulls (paths with no declared
 * room) and collapse consecutive duplicates (a compound state's parent + child
 * entries resolve to the same room).
 *
 * Source / truth status: TRACE — ground truth, the run went there.
 */
export function traveledPath(events: StateEnteredish[], diagram: Diagram | null): string[] {
  if (!diagram) return [];
  const out: string[] = [];
  for (const e of events) {
    if (e.msg !== "machine.state_entered") continue;
    const rid = matchRoomId(e.state_path, diagram);
    if (!rid) continue;
    if (out.length > 0 && out[out.length - 1] === rid) continue;
    out.push(rid);
  }
  return out;
}

/** Classification of a horizon arc relative to the current room. */
export type HorizonKind = "forward" | "self" | "exit";

/** One live move available from the current room. */
export interface HorizonArc {
  /** The intent name to submit. */
  intent: string;
  /** Display label (intent title, else the name). */
  label: string;
  /** The room this intent lands in, or null when no outgoing edge matches. */
  targetRoomId: string | null;
  kind: HorizonKind;
}

// Escape-intent names: quit / cancel / abort / leave (exact), plus the quit_*
// family (e.g. quit_to_menu). `leave` is matched exactly, NOT as a prefix, so a
// forward intent like `leave_store` ("leave the store and start the journey")
// is not misread as an escape.
const EXIT_INTENT_RE = /^(quit(_.*)?|cancel|abort|leave)$/;

/**
 * The live exits from the current room: the allowed intents joined to the
 * room's parsed outgoing edges (edge.label === intent.name, self-loops
 * included). Every allowed intent appears, even one with no matching edge
 * (targetRoomId=null) — the live menu is authoritative about what you can do.
 *
 * kind:
 *   - self    — a self-loop, or an edge whose target IS the current room.
 *   - exit    — target is the hub/root room, OR the intent name is a known
 *               escape (quit / cancel / abort / leave / quit_*).
 *   - forward — anything else (a genuine advance).
 *
 * Source / truth status: LIVE — ground truth, what you can do now.
 */
export function horizon(
  intents: HorizonIntent[],
  currentRoomId: string | null,
  diagram: Diagram | null,
): HorizonArc[] {
  if (!diagram || !currentRoomId) return [];

  // Outgoing edges for the current room, keyed by intent label. Self-loops are
  // KEPT here (unlike StateDiagram's outgoingByRoom) because a self-loop is a
  // legitimate live move that must surface as a pill.
  const byLabel = new Map<string, Edge>();
  for (const e of diagram.edges) {
    if (e.from !== currentRoomId) continue;
    if (e.label && !byLabel.has(e.label)) byLabel.set(e.label, e);
  }

  const hub = diagram.startRoomId;

  return intents.map((it) => {
    const edge = byLabel.get(it.name);
    const targetRoomId = edge ? edge.to : null;
    const isSelf =
      (edge?.selfLoop ?? false) || (targetRoomId !== null && targetRoomId === currentRoomId);
    const isExit =
      EXIT_INTENT_RE.test(it.name) || (targetRoomId !== null && hub !== null && targetRoomId === hub);
    const kind: HorizonKind = isSelf ? "self" : isExit ? "exit" : "forward";
    return {
      intent: it.name,
      label: it.title?.trim() || it.name,
      targetRoomId,
      kind,
    };
  });
}

/** A unique forward spine of phases beyond the current one. */
export interface SpineAhead {
  kind: "spine";
  phases: Phase[];
}
/** No unique spine: ≥2 phases sit at equal minimum forward distance. */
export interface BranchesAhead {
  kind: "branches";
  count: number;
}

/**
 * The road ahead, projected from the STATIC graph: the forward-reachable phases
 * AFTER the current phase, ordered by Phase.phaseNumber (fallback Room.distance).
 * `diagram.phases` is already topologically ordered by parse.ts, so "after the
 * current phase" is the slice past the current phase's index — minus exit
 * pseudo-phases (`__exit__*`, "ended") which aren't road, they're the terminus.
 *
 * If ≥2 phases sit at the SAME minimum forward distance immediately ahead — a
 * genuine branch with no unique spine — we return the {kind:"branches"} form
 * rather than fabricating a line that would read as a single declared route.
 *
 * Source / truth status: PROJECTION — what the machine declares, not what ran.
 * MUST be styled distinctly from traveled (never solid/bright).
 */
export function spineAhead(currentRoomId: string | null, diagram: Diagram | null): SpineAhead | BranchesAhead {
  if (!diagram || !currentRoomId) return { kind: "spine", phases: [] };

  const curPhaseId = diagram.phaseByRoom.get(currentRoomId);
  if (!curPhaseId) return { kind: "spine", phases: [] };

  const curIdx = diagram.phases.findIndex((p) => p.id === curPhaseId);
  if (curIdx < 0) return { kind: "spine", phases: [] };

  const isExit = (p: Phase): boolean => p.name.startsWith("__exit__") || p.name === "ended";

  // Phases strictly after the current one in the topo order, excluding exits.
  const ahead = diagram.phases.slice(curIdx + 1).filter((p) => !isExit(p));
  if (ahead.length === 0) return { kind: "spine", phases: [] };

  // Branch detection: do ≥2 phases share the minimum forward rank? Rank by
  // phaseNumber when every ahead-phase declares one, else by Room.distance.
  const rankOf = (p: Phase): number => {
    if (p.phaseNumber != null) return p.phaseNumber;
    let m = Infinity;
    for (const r of p.rooms) if (r.distance < m) m = r.distance;
    return m;
  };
  let minRank = Infinity;
  let minCount = 0;
  for (const p of ahead) {
    const r = rankOf(p);
    if (r < minRank) {
      minRank = r;
      minCount = 1;
    } else if (r === minRank) {
      minCount += 1;
    }
  }
  if (minCount >= 2) {
    return { kind: "branches", count: ahead.length };
  }

  return { kind: "spine", phases: ahead };
}

/** The road ahead as a sequence of ROOMS (not phases). */
export interface RoomSpine {
  /** Upcoming rooms in projected order (current room excluded). */
  rooms: Room[];
  /** True when at any step ≥2 forward candidates existed — the line is the
   *  dominant route, but real alternatives exist (shown as horizon pills). */
  branched: boolean;
}

/**
 * The road ahead at ROOM granularity — needed when a whole pipeline lives in a
 * single phase (dev-story's `design_*` rooms are all one phase, so the
 * phase-keyed {@link spineAhead} would show nothing ahead).
 *
 * It is a greedy forward walk from the current room: at each step take the
 * outgoing edge to the DEEPEST unvisited room that is neither a self-loop, the
 * hub, nor an escape intent (quit/cancel/abort/leave), breaking ties by label.
 * `visited` (typically the traveled path) seeds the cycle guard so the
 * projection never loops back onto rooms the run already left. "Deepest" =
 * greatest BFS Room.distance, which prefers advancing along the pipeline over a
 * shortcut hop; the cycle guard makes it terminate.
 *
 * Greedy (not longest-path) is deliberate: it's O(rooms), deterministic, and
 * for the linear-with-shortcuts pipelines this view targets it recovers the
 * canonical route. `branched` flags that alternatives existed so the caller can
 * hint without the projection pretending the fork away.
 *
 * Source / truth status: PROJECTION — the declared forward region, not what
 * ran. MUST be styled muted/dashed, never as traveled.
 */
export function roomSpineAhead(
  currentRoomId: string | null,
  diagram: Diagram | null,
  visited: string[] = [],
): RoomSpine {
  if (!diagram || !currentRoomId) return { rooms: [], branched: false };
  const hub = diagram.startRoomId;
  const seen = new Set<string>(visited);
  seen.add(currentRoomId);

  // Outgoing forward candidates from a room (real, non-self, non-hub,
  // non-escape, unseen targets), as {to, distance}.
  const forwardCands = (roomId: string): Room[] => {
    const out: Room[] = [];
    const taken = new Set<string>();
    for (const e of diagram.edges) {
      if (e.from !== roomId || e.selfLoop) continue;
      if (EXIT_INTENT_RE.test(e.label)) continue;
      const to = diagram.roomById.get(e.to);
      if (!to || to.id === hub || seen.has(to.id) || taken.has(to.id)) continue;
      taken.add(to.id);
      out.push(to);
    }
    return out;
  };

  const rooms: Room[] = [];
  let branched = false;
  let node = currentRoomId;
  // Cap the walk at the room count — the seen-set already guarantees
  // termination; the cap is a belt-and-braces guard against malformed graphs.
  for (let i = 0; i < diagram.roomById.size; i++) {
    const cands = forwardCands(node);
    if (cands.length === 0) break;
    if (cands.length > 1) branched = true;
    // Deepest unvisited wins; ties broken by label for determinism.
    cands.sort((a, b) => (b.distance - a.distance) || a.label.localeCompare(b.label));
    const next = cands[0]!;
    rooms.push(next);
    seen.add(next.id);
    node = next.id;
  }
  return { rooms, branched };
}

/** The fields of a `machine.transition` event {@link enteringIntents} reads. */
export interface Transitionish {
  msg: string;
  attrs?: { intent?: unknown; to?: unknown };
}

/**
 * Maps each room the run ENTERED to the intent that drove the entry, from the
 * `machine.transition` event stream (attrs.intent is the triggering intent,
 * attrs.to the landed state). Used for the breadcrumb provenance ("main →
 * design *via go_idea*"). The most recent transition into a room wins.
 *
 * Source / truth status: TRACE — ground truth, the intent the run actually fired.
 */
export function enteringIntents(events: Transitionish[], diagram: Diagram | null): Map<string, string> {
  const out = new Map<string, string>();
  if (!diagram) return out;
  for (const e of events) {
    if (e.msg !== "machine.transition") continue;
    const to = typeof e.attrs?.to === "string" ? e.attrs.to : null;
    const intent = typeof e.attrs?.intent === "string" ? e.attrs.intent : null;
    if (!to || !intent) continue;
    const rid = matchRoomId(to, diagram);
    if (rid) out.set(rid, intent);
  }
  return out;
}

/** Re-exported so a consumer needn't import parse.ts separately. */
export type { Phase, Room, Edge, Diagram };
