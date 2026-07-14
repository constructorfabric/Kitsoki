/**
 * Embed boot params (portal-kitsoki-chat-embed-plan.md §3.1) — the
 * presentation-free URL contract a host page uses to pin an embedded
 * ChatSurface to a story/scope/catalog and skip the picker:
 *
 *     <kitsoki-origin>/?surface=chat&embed=1
 *       &story=<path-or-app-id>&autostart=1&catalog=<alias>&scope=<key>
 *       [&world_seed=<URI-encoded JSON object>]
 *
 * `story`/`autostart` seed ChatSurface's existing selectedStoryPath/onStart.
 * `catalog`/`scope` ride session.new's initial_world (world.catalog,
 * world.scope_key) — never the URL beyond this one query read.
 *
 * `world_seed` (U4, feedback report 01KXD23CJBVXQGQ08EXET3QC1X) carries the
 * richer context payload (node_ids, filters, instruction, ...) IN the boot
 * request instead of the first `context` postMessage, closing the residual
 * autostart race: the postMessage path waits at most a fixed window
 * (embedHost.waitForContext's timeout) before autostart's session.new
 * proceeds, so a host that posts context late silently loses it. A
 * URL-delivered seed exists before any script runs and cannot lose that
 * race. When both are supplied, world_seed wins and the postMessage wait
 * is skipped.
 */

export interface EmbedBoot {
  story: string | null;
  autostart: boolean;
  catalog: string | null;
  scope: string | null;
  /** Explicit postMessage target origin override (embedHost.ts). */
  origin: string | null;
  /**
   * Pre-delivered portal context (the same payload shape the host would
   * post as its `context` message). null when absent or unparseable — a
   * malformed seed degrades to the postMessage path, never throws.
   */
  worldSeed: Record<string, unknown> | null;
}

function parseWorldSeed(raw: string | null): Record<string, unknown> | null {
  if (!raw) return null;
  try {
    const parsed: unknown = JSON.parse(raw);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return parsed as Record<string, unknown>;
    }
  } catch {
    // fall through — a bad seed must not break boot
  }
  return null;
}

export function resolveEmbedBoot(search: string = location.search): EmbedBoot {
  const params = new URLSearchParams(search);
  return {
    story: params.get("story"),
    autostart: params.get("autostart") === "1",
    catalog: params.get("catalog"),
    scope: params.get("scope"),
    origin: params.get("origin"),
    worldSeed: parseWorldSeed(params.get("world_seed")),
  };
}
