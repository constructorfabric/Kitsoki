/**
 * semanticPlugins — the client-side reader + plugin registry for an artifact's
 * semantic sidecar, mirroring the Go envelope in internal/host/semantic_sidecar.go
 * (SemanticSidecar / SemanticElement) EXACTLY.
 *
 * A producer (slidey today; any media producer tomorrow) emits a
 * `<name>.semantic.json` sidecar alongside its artifact, declaring the named,
 * clickable elements inside it. The envelope is producer-AGNOSTIC: a top-level
 * `plugin` names the producer, and each element carries an opaque `ref` that
 * kitsoki round-trips VERBATIM into a `semantic_element` anchor — it never
 * interprets `ref`. `label` / `selector` / `bbox` / `t_ms` are optional overlay
 * hints the picker uses.
 *
 * The client registry maps a plugin → an optional label formatter (presentation
 * only — it never changes the emitted `ref`). An ABSENT plugin entry means
 * "render the element generically" (its own `label`, else its `ref`), so an
 * unknown future plugin still annotates.
 */

/** One element declared by a semantic sidecar — mirrors host.SemanticElement.
 *  `ref` is the opaque producer reference (round-tripped into the anchor); the
 *  rest are optional hints. `bbox` is [x,y,w,h] in the media's natural pixels. */
export interface SemanticElement {
  /** The producer's opaque element id (the anchor's `ref`). */
  ref: string;
  /** Human-readable name for the marker (else `ref`). */
  label?: string;
  /** Optional DOM/CSS selector for html/rrweb artifacts. */
  selector?: string;
  /** The element's box [x,y,w,h] in natural pixels, when supplied. */
  bbox?: [number, number, number, number];
  /** The element's timestamp in ms for timeline-based artifacts. */
  t_ms?: number;
}

/**
 * SemanticSidecar — the parsed `<name>.semantic.json` envelope (mirrors
 * host.SemanticSidecar). `plugin` is the producer; `elements` the declared
 * clickable elements. The sidecar does NOT carry a natural-size header in the Go
 * shape, so the overlay scales element boxes against the media's intrinsic size
 * it learns from the rendered element (img/video naturalWidth).
 */
export interface SemanticSidecar {
  plugin: string;
  schema_version?: number;
  elements: SemanticElement[];
}

/**
 * SemanticMap — the overlay-facing view of a sidecar: the producer plugin, the
 * natural pixel space the boxes are expressed in (so the overlay can position
 * markers as a percent), and the elements. Built from a SemanticSidecar +
 * the media's known natural size via `toSemanticMap`.
 */
export interface SemanticMap {
  /** The artifact handle these elements describe. */
  media: string;
  /** The producer that emitted the sidecar (the registry key). */
  plugin: string;
  /** The natural pixel space the element boxes are expressed in. */
  natural: { width: number; height: number };
  /** The clickable elements. */
  elements: SemanticElement[];
}

/**
 * semanticSidecarName derives the sidecar filename for an artifact handle: the
 * handle's basename with any extension replaced by `.semantic.json`. A
 * content-addressed handle (e.g. `deck#6e2b0759` or `deck.mp4`) becomes
 * `deck.semantic.json` — the same pairing rule host.SemanticSidecarPath uses.
 * Pure (shared by SnapshotSource's path build and the unit tests).
 */
export function semanticSidecarName(handle: string): string {
  const base = handle.split("/").pop() ?? handle;
  const stem = base.split("#")[0].replace(/\.[^.]+$/, "");
  return `${stem}.semantic.json`;
}

/**
 * toSemanticMap adapts a parsed SemanticSidecar (the wire envelope) + the media
 * handle + its natural size into the overlay-facing SemanticMap. Elements with no
 * bbox are kept (the overlay skips drawing a box but still lists them).
 */
export function toSemanticMap(
  sidecar: SemanticSidecar,
  media: string,
  natural: { width: number; height: number }
): SemanticMap {
  return {
    media,
    plugin: sidecar.plugin,
    natural,
    elements: sidecar.elements ?? [],
  };
}

/** A label formatter: given an element + its plugin, return the marker label. */
export type SemanticLabelFormatter = (
  el: SemanticElement,
  plugin: string
) => string;

/** Optional per-plugin presentation hooks. Absent ⇒ generic rendering. */
export interface SemanticPlugin {
  /** Customize the marker label (absent ⇒ element.label ?? element.ref). */
  label?: SemanticLabelFormatter;
}

/**
 * The default registry, keyed by the sidecar's `plugin`. Slidey is the v2 PoC
 * producer; a slidey ref is `<scene>.<role>` (e.g. "scene-3.title"), so the
 * formatter humanizes it when no explicit label is given.
 */
export const semanticPlugins: Record<string, SemanticPlugin> = {
  slidey: {
    label: (el) => {
      if (el.label) return el.label;
      // "scene-3.title" → "scene-3 · title" (presentation only; ref unchanged).
      const dot = el.ref.indexOf(".");
      if (dot > 0) {
        return `${el.ref.slice(0, dot)} · ${el.ref.slice(dot + 1)}`;
      }
      return el.ref;
    },
  },
};

/**
 * formatLabel resolves an element's display label through the registry: a
 * registered plugin's formatter, else the element's own `label`, else its `ref`.
 * Pure; safe for an unknown plugin (the generic fallback).
 */
export function formatLabel(
  el: SemanticElement,
  plugin: string,
  registry: Record<string, SemanticPlugin> = semanticPlugins
): string {
  const p = registry[plugin];
  if (p?.label) return p.label(el, plugin);
  return el.label ?? el.ref;
}
