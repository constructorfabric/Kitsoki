/**
 * clipBug — a demo-only query flag that restores a since-fixed layout
 * regression in the Agent Actions drawer, so a deterministic before/after
 * rrweb capture can show the bug and its fix side by side.
 *
 * The regression: an agent-action row title is a flex child without
 * `min-width: 0`. A flex item's implicit minimum is its content size, so a long
 * title (e.g. a verbose reasoning summary) refuses to shrink and its
 * `text-overflow: ellipsis` never engages — the title overflows its card and
 * shoves the token/cost cluster off the row. The fix is the canonical flexbox
 * remedy: `min-width: 0` on the flex child (see AgentActionRow.vue `.aar__title`).
 *
 * When opened with `?clipBug=1` the drawer re-adds the pre-fix CSS (the BEFORE
 * state). The flag is inert for every normal session — it only re-enables a bug
 * for the bugfix-deck capture spec and is read, like `chromeless`, from
 * `window.location.search` so it survives the hash route.
 */

/** isClipBug reports whether the page was opened with `?clipBug=1`. */
export function isClipBug(): boolean {
  if (typeof window === "undefined") return false;
  return new URLSearchParams(window.location.search).get("clipBug") === "1";
}
