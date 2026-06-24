/**
 * annotationAnchor — the v2 generalization of the spatial-oracle VisualBundle
 * from rrweb-only to png / mp4 / rrweb / html / slidey, mirroring the Go wire
 * contract in internal/host/annotation_anchor.go (AnnotationAnchor /
 * AnchorFromParams). This is the canonical shape every slice integrates against;
 * the field names + nesting here match the Go JSON tags EXACTLY so an anchor
 * round-trips through `runstatus.session.offpath`'s `anchor` param and the
 * recorded trace block without translation.
 *
 * Spatial-oracle's `VisualBundle` carried a flat point + element + frame_handle
 * bundle that only ever described "a DOM node under a click in an rrweb frame".
 * The unified feature points at FIVE media kinds, each with its own locator:
 *
 *   - png   → a `region` (box / freeform / highlight) on a still image
 *   - mp4   → a `time_range`, or a `frame` grab, then a `region` on that still
 *   - rrweb → a `dom_node` (reconstructed element) or a `region` / `time_range`
 *   - html  → a `dom_node` (a static iframe element) or a `region`
 *   - slidey→ a `semantic_element` (a sidecar-declared element) or a `dom_node`
 *
 * The on-wire union (AnchorWire) is a tagged shape: a top-level `kind` plus a
 * SIBLING object named by the kind (`anchor.dom_node`, `anchor.region`, …) —
 * the exact shape host.AnchorFromParams decodes. The component layer works in a
 * slightly richer in-memory `AnnotationAnchor` (it keeps a `point` + a
 * `semantic_element.label` the UI uses); `serializeAnchor` projects that down to
 * the wire shape kitsoki round-trips.
 */
import type { ResolvedElement } from "./resolveElement.js";

/** The artifact kinds the annotator can point at. `slidey` is a slideshow
 *  (declarative video) artifact whose elements come from a semantic sidecar. */
export type MediaKind = "png" | "mp4" | "rrweb" | "html" | "slidey";

/** The discriminator naming which target is populated (mirrors host.AnchorKind).
 *  `frame` is a single grabbed still; the picker kinds add the rest. */
export type AnchorKind =
  | "time_range"
  | "frame"
  | "dom_node"
  | "region"
  | "semantic_element";

/** Shapes a freehand/region draw can take (mirrors host.RegionShape). */
export type RegionShape = "box" | "freeform" | "highlight";

/** A box / rect in the media's natural pixel space. */
export interface Box {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** A point in the media's natural pixel space. */
export interface Point {
  x: number;
  y: number;
}

// ── In-memory target variants (the component-facing union) ───────────────────

/** A reconstructed/static DOM element (rrweb contentDocument or html iframe). */
export interface DomNodeTarget {
  kind: "dom_node";
  element: ResolvedElement;
  /** The originating click point, in frame pixels (UI marker; not on the wire). */
  point?: Point;
}

/** A drawn region on a still. `box`/`highlight` carry a bbox; `freeform` also
 *  carries the ordered path of points (its bbox is the path's bounding box). */
export interface RegionTarget {
  kind: "region";
  shape: RegionShape;
  bbox: Box;
  path?: Point[];
  point?: Point;
}

/** A time window within a video (mp4 / rrweb). `end_ms` absent ⇒ an instant. */
export interface TimeRangeTarget {
  kind: "time_range";
  start_ms: number;
  end_ms?: number;
}

/** A single grabbed still referenced by handle (an mp4 frame grab). */
export interface FrameTarget {
  kind: "frame";
  frame_handle: string;
  t_ms?: number;
}

/**
 * A sidecar-declared semantic element. `ref` is the opaque element reference the
 * producer declared (kitsoki round-trips it verbatim — it never interprets it);
 * `plugin` names the producer; `bbox` is the element's box. `id` and `label` are
 * UI-only conveniences (the marker's key + display) and do NOT ride the wire.
 */
export interface SemanticElementTarget {
  kind: "semantic_element";
  plugin: string;
  ref: string;
  bbox?: Box;
  /** UI-only: the marker's stable key (defaults to `ref`). */
  id?: string;
  /** UI-only: the formatted display label. */
  label?: string;
  /** UI-only: the box's anchor point. */
  point?: Point;
}

export type AnnotationTarget =
  | DomNodeTarget
  | RegionTarget
  | TimeRangeTarget
  | FrameTarget
  | SemanticElementTarget;

/**
 * AnnotationAnchor — the component-facing unified attachment. Rides on
 * `runstatus.session.offpath`'s `anchor` param (serialized via serializeAnchor)
 * and the feedback note's `anchor`.
 */
export interface AnnotationAnchor {
  /** The artifact handle the operator annotated. */
  media_handle?: string;
  /** Which media kind it is — drives the annotator's render dispatch. */
  media_kind?: MediaKind;
  /** A captured still's artifact handle (an mp4 frame-grab, or a png itself). */
  frame_handle?: string;
  /** WHAT inside the media the annotation points at. */
  target?: AnnotationTarget;
  /** The route the capture happened on (e.g. "/review/<sid>"). */
  route?: string;
}

// ── The on-wire shape (mirrors host.AnnotationAnchor JSON / AnchorFromParams) ─

/** The wire `anchor` object: a top-level `kind` + a sibling named by the kind.
 *  bbox is a positional [x,y,w,h]; path is a positional [[x,y],…] list. */
export interface AnchorWire {
  kind: AnchorKind;
  time_range?: { start_ms: number; end_ms?: number };
  frame?: { frame_handle: string; t_ms?: number };
  dom_node?: { selector: string; role: string; text: string; bbox: [number, number, number, number] };
  region?: { shape: RegionShape; path: [number, number][]; bbox: [number, number, number, number] };
  semantic_element?: { plugin: string; ref: string; bbox?: [number, number, number, number] };
}

function bboxTuple(b: Box): [number, number, number, number] {
  return [b.x, b.y, b.width, b.height];
}

/**
 * serializeAnchor projects the component-facing AnnotationAnchor's `target` into
 * the on-wire AnchorWire object host.AnchorFromParams decodes — the SIBLING-named
 * tagged shape (`{kind, dom_node:{…}}`), bbox/path flattened to positional
 * arrays. Returns null for an anchor with no target (a v1-only bundle sends no
 * `anchor` key, and the server synthesizes one from the flat fields).
 */
export function serializeAnchor(anchor: AnnotationAnchor): AnchorWire | null {
  const t = anchor.target;
  if (!t) return null;
  switch (t.kind) {
    case "time_range":
      return {
        kind: "time_range",
        time_range: { start_ms: t.start_ms, ...(t.end_ms ? { end_ms: t.end_ms } : {}) },
      };
    case "frame":
      return {
        kind: "frame",
        frame: { frame_handle: t.frame_handle, ...(t.t_ms ? { t_ms: t.t_ms } : {}) },
      };
    case "dom_node":
      return {
        kind: "dom_node",
        dom_node: {
          selector: t.element.selector,
          role: t.element.role,
          text: t.element.text,
          bbox: bboxTuple(t.element.bbox),
        },
      };
    case "region":
      return {
        kind: "region",
        region: {
          shape: t.shape,
          path: (t.path ?? []).map((p) => [p.x, p.y] as [number, number]),
          bbox: bboxTuple(t.bbox),
        },
      };
    case "semantic_element":
      return {
        kind: "semantic_element",
        semantic_element: {
          plugin: t.plugin,
          ref: t.ref,
          ...(t.bbox ? { bbox: bboxTuple(t.bbox) } : {}),
        },
      };
  }
}

// ── Normalization from the legacy picker bundle ──────────────────────────────

/** The flat bundle SpatialPicker / ReplayFrame still emit (point/box/element). */
export interface PickerBundle {
  point: Point;
  box?: Box;
  element?: ResolvedElement;
}

/**
 * normalizeAnchor maps the current picker output (a flat point/box/element
 * bundle) into the discriminated `target`, choosing the variant by what the
 * picker resolved — preserving the spatial-oracle semantics exactly:
 *
 *   - a resolved `element`  → `dom_node`   (the strongest locator; keep the point)
 *   - a drag `box` (no elem)→ `region`/box (a drawn rectangle on the still)
 *   - point only            → `region`/highlight at a 1×1 box (a pin)
 */
export function normalizeAnchor(
  bundle: PickerBundle,
  meta: Omit<AnnotationAnchor, "target"> = {}
): AnnotationAnchor {
  let target: AnnotationTarget;
  if (bundle.element) {
    target = { kind: "dom_node", element: bundle.element, point: bundle.point };
  } else if (bundle.box) {
    target = {
      kind: "region",
      shape: "box",
      bbox: bundle.box,
      point: { x: bundle.box.x, y: bundle.box.y },
    };
  } else {
    target = {
      kind: "region",
      shape: "highlight",
      bbox: { x: bundle.point.x, y: bundle.point.y, width: 1, height: 1 },
      point: bundle.point,
    };
  }
  return { ...meta, target };
}

/**
 * regionToTarget builds a `region` target from a finished canvas draw (the
 * RegionDrawLayer emit). A `freeform` keeps its path; a `box`/`highlight` carries
 * the bbox and its top-left anchor.
 */
export function regionToTarget(region: {
  shape: RegionShape;
  bbox: Box;
  path?: Point[];
}): RegionTarget {
  return {
    kind: "region",
    shape: region.shape,
    bbox: region.bbox,
    ...(region.shape === "freeform" && region.path ? { path: region.path } : {}),
    point: { x: region.bbox.x, y: region.bbox.y },
  };
}

/**
 * anchorToVisualBundle — the back-compat projection to the legacy VisualBundle
 * shape, so the existing `offpath(..., visual)` param keeps carrying context for
 * a server reading the flat fields. A `dom_node` carries point+element; a
 * `region`/`semantic_element` carries its point; `time_range`/`frame` carry the
 * still + t_ms.
 */
export function anchorToVisualBundle(anchor: AnnotationAnchor): {
  frame_handle?: string;
  media_handle?: string;
  point?: Point;
  element?: ResolvedElement;
  t_ms?: number;
  route?: string;
} {
  const out: {
    frame_handle?: string;
    media_handle?: string;
    point?: Point;
    element?: ResolvedElement;
    t_ms?: number;
    route?: string;
  } = {};
  if (anchor.frame_handle) out.frame_handle = anchor.frame_handle;
  if (anchor.media_handle) out.media_handle = anchor.media_handle;
  if (anchor.route) out.route = anchor.route;
  const t = anchor.target;
  if (t) {
    switch (t.kind) {
      case "dom_node":
        if (t.point) out.point = t.point;
        out.element = t.element;
        break;
      case "region":
      case "semantic_element":
        if (t.point) out.point = t.point;
        break;
      case "time_range":
        out.t_ms = t.start_ms;
        break;
      case "frame":
        out.frame_handle = t.frame_handle;
        if (t.t_ms) out.t_ms = t.t_ms;
        break;
    }
  }
  return out;
}
