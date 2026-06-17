/**
 * chromeless — the query flag the SPA root reads to render the transient,
 * single-purpose `/point` spatial-handoff window
 * (docs/tui/spatial-handoff.md) instead of the full app
 * shell (nav / timeline / editor / meta / tour / inbox).
 *
 * The terminal hands a TUI operator an OSC 8 link to
 * `/point?token=…&chromeless=1`. The server serves the same bundled SPA there;
 * this flag is what makes that SPA boot into the stripped chrome-less mode — so
 * the flag is inert for the normal SPA (every other route ignores it) and the
 * handoff window needs no separate bundle.
 *
 * It reads `window.location.search` (NOT the hash route): `/point` is a real
 * server path, not a hash route, and the token + flag ride on its query string.
 */

/** isChromeless reports whether the page was opened with `?chromeless=1`. */
export function isChromeless(): boolean {
  if (typeof window === "undefined") return false;
  return new URLSearchParams(window.location.search).get("chromeless") === "1";
}

/** pointToken returns the one-time `?token=…` the `/point` window was opened
 * with (empty when absent). The return POST echoes it so the server can match
 * the bundle to the parked turn. */
export function pointToken(): string {
  if (typeof window === "undefined") return "";
  return new URLSearchParams(window.location.search).get("token") ?? "";
}
