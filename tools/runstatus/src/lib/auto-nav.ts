/**
 * Auto-navigation guard for the home screen.
 *
 * When exactly one live session exists, the home screen ("/") opens straight
 * into it as a convenience. That redirect must fire AT MOST ONCE per browser
 * tab: after the first arrival the user has chosen to be on a screen — whether
 * the stories list (via "← Stories") or a session view — and bouncing them back
 * into the lone session would make starting a *new* story impossible (the
 * "once one is started, that's it" trap).
 *
 * Two things make the guard robust:
 *
 *  1. It is persisted in sessionStorage, not a module-level `let`. A hard reload
 *     (exactly what happens when the user edits the URL back to "/") re-imports
 *     this module and would reset an in-memory flag to false, re-triggering the
 *     redirect. sessionStorage survives reloads but is scoped to the tab, so a
 *     freshly opened tab still gets the open-into-the-one-session convenience.
 *
 *  2. It is marked done not only by the home screen but by every session view on
 *     mount (see InteractiveView / RunView). Otherwise a tab whose FIRST mount is
 *     a session URL (a pasted/bookmarked link, or the push right after starting a
 *     session) never sets the flag, so the user's first "← Stories" click — with
 *     one live session — would bounce them straight back in. Once the user is
 *     already viewing a session the auto-redirect has nothing left to do, so
 *     marking it spent there is always correct.
 */
const AUTO_NAV_KEY = "kitsoki.home.autoNavDone";

/** True once the per-tab auto-navigation has already fired (or been spent). */
export function autoNavDone(): boolean {
  try {
    return sessionStorage.getItem(AUTO_NAV_KEY) === "1";
  } catch {
    // Private-mode / disabled storage: degrade to "always show home" rather
    // than risk trapping the user in the redirect loop.
    return true;
  }
}

/** Mark the per-tab auto-navigation as spent so it never fires again. */
export function markAutoNavDone(): void {
  try {
    sessionStorage.setItem(AUTO_NAV_KEY, "1");
  } catch {
    /* storage unavailable — autoNavDone() already returns true in that case */
  }
}
