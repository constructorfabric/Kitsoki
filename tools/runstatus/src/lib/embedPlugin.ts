/**
 * embedPlugin — the artifact EMBEDDING + spatial-feedback plugin contract.
 *
 * Replaces the old hardcoded-iframe annotator. An embed plugin owns ALL of:
 *   1. rendering an artifact (display or annotate mode) into a container,
 *   2. generating spatial feedback — letting the operator point at a place and
 *      emitting ONE producer-agnostic AnnotationAnchor, and
 *   3. reporting the currently-viewed location (e.g. "scene 9") so the rest of
 *      kitsoki can gate an edit to the slide the operator is looking at.
 *
 * kitsoki ships built-in plugins for the generic kinds it understands — `png`,
 * `mp4`, `html`. A producer with richer structure (a multi-scene deck, a CAD
 * model, a notebook) ships its OWN plugin and the STORY references it; kitsoki
 * core stays ignorant of that producer. studio-slidey provides the `slidey`
 * plugin this way — kitsoki knows nothing about slidey.
 *
 * The contract is deliberately tiny and framework-free (plain DOM + callbacks)
 * so an external plugin needs no Vue/kitsoki build coupling: it gets a container
 * element and a host of DI services, and returns a teardown handle.
 */

import type { AnnotationAnchor } from "./annotationAnchor.js";
import type { SemanticSidecar } from "./semanticPlugins.js";

/**
 * EmbedView — the location a plugin is currently showing. Opaque to kitsoki
 * except `label` (display) and `scope` (an opaque token the producer uses to
 * SCOPE an edit — e.g. a slidey scene index "9"). kitsoki never interprets
 * `scope`; it round-trips it onto the dispatched refine so the producer's own
 * gate can reject edits that stray off the viewed location.
 */
export interface EmbedView {
  /** Human label for the current view, e.g. "Scene 9 · Cat Wrangling". */
  label?: string;
  /** Opaque producer scope token for the current view (e.g. "9"). */
  scope?: string;
  [k: string]: unknown;
}

/**
 * EmbedHost — the DI services kitsoki hands a plugin. No globals: a plugin
 * reaches kitsoki ONLY through this, so it is trivially testable and an external
 * plugin has no implicit coupling.
 */
export interface EmbedHost {
  /** The artifact handle being embedded. */
  mediaHandle: string;
  /** Resolve an artifact handle to a fetchable URL. */
  artifactUrl(handle: string): string;
  /** Resolve the sibling poster still URL for a handle, when the source has one. */
  posterUrl?(handle: string): string;
  /**
   * Fetch the producer-agnostic semantic sidecar for a handle (or null when the
   * artifact has none). The plugin owns interpreting it — kitsoki only ferries
   * the opaque envelope.
   */
  fetchSemantic?(handle: string): Promise<SemanticSidecar | null>;
  /** The plugin calls this when the operator picks a spot to annotate. */
  emitAnchor(anchor: AnnotationAnchor): void;
  /**
   * The plugin reports the currently-viewed location (e.g. the scene the deck
   * navigated to). kitsoki carries `scope` onto the refine so the edit is gated
   * to it. Optional: a flat artifact (a png) has a single implicit view.
   */
  reportView?(view: EmbedView): void;
}

/** How the embed is mounted: display-only, or interactive annotation. */
export type EmbedMode = "display" | "annotate";

export interface EmbedParams {
  /** kitsoki's media-kind hint (png|mp4|html|slidey|…). A plugin may ignore it. */
  mediaKind: string;
  /** display = read-only preview; annotate = pick-a-spot spatial feedback. */
  mode: EmbedMode;
  /** Optional poster/backdrop handle (e.g. a deck frame still). */
  posterHandle?: string;
}

/** A mounted embed. kitsoki calls destroy() on teardown / handle change. */
export interface EmbedInstance {
  /** Tear down listeners, iframes, object URLs, etc. Must be idempotent. */
  destroy(): void;
  /**
   * Optional: re-point the embed at a new artifact handle WITHOUT a full
   * remount (a refine re-render swaps the handle). When absent, kitsoki
   * destroys + remounts instead.
   */
  update?(mediaHandle: string): void;
}

/**
 * EmbedPlugin — claims one or more media kinds and mounts a substrate. `mount`
 * may be async (a plugin can fetch a sidecar / lazy-load a runtime first).
 */
export interface EmbedPlugin {
  /** Stable, unique id, e.g. "kitsoki.html", "studio.slidey". */
  id: string;
  /** The media kinds this plugin handles. */
  kinds: string[];
  /** Mount the substrate into `container`. */
  mount(
    container: HTMLElement,
    params: EmbedParams,
    host: EmbedHost,
  ): EmbedInstance | Promise<EmbedInstance>;
}

// ── Registry ──────────────────────────────────────────────────────────────────
//
// A process-wide map of media-kind → plugin. kitsoki registers its built-ins at
// startup; a story registers an external producer's plugin when it loads (the
// story references the plugin module). Last registration for a kind wins, so a
// story can OVERRIDE a built-in for its own richer substrate.

const registry = new Map<string, EmbedPlugin>();

/** Register a plugin for each of its `kinds`. Idempotent per (kind, plugin). */
export function registerEmbedPlugin(plugin: EmbedPlugin): void {
  for (const kind of plugin.kinds) {
    registry.set(kind, plugin);
  }
}

/** Resolve the plugin for a media kind, or null when none is registered. */
export function resolveEmbedPlugin(kind: string): EmbedPlugin | null {
  return registry.get(kind) ?? null;
}

/** Whether a media kind has a registered embed plugin. */
export function hasEmbedPlugin(kind: string): boolean {
  return registry.has(kind);
}

/** Test/teardown helper: forget all registrations. */
export function clearEmbedPlugins(): void {
  registry.clear();
}

/** The currently-registered plugin ids (for diagnostics / tests). */
export function registeredEmbedKinds(): string[] {
  return [...registry.keys()];
}
