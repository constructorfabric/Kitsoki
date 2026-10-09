/**
 * isolation.ts — the STRICT per-user session-isolation assertion.
 *
 * Ground truth: each swarm user submits a unique, unguessable marker string
 * as their first free-text message (see journey.ts's `discuss` step). The
 * PRD story's `idle` room view never echoes that text back into the
 * rendered room view (only the canned agent reply is templated in), and the
 * web UI's composer transcript is a per-tab local echo, not something a
 * SECOND page reliably rehydrates on load — so neither is a safe
 * cross-page ground truth (an early version of this check read
 * `page.locator("body").innerText()` and produced a false negative for
 * exactly this reason).
 *
 * The one thing that IS guaranteed to carry the raw submitted text, is
 * scoped to exactly one session, and is independently readable by ANY page
 * (or no page at all) is the session's own JOURNALED TRACE
 * (`runstatus.session.trace`): the `discuss` arc's `set: { idea_message:
 * "{{ slots.message }}" }` effect is written into a `world.update` trace
 * event verbatim (confirmed with a bare RPC probe — no browser, no UI).
 * This also matches tools/runstatus/AGENTS.md's own standing rule for this
 * viewer: "never use a UI hack ... the trace itself must always be
 * correct" — the trace, not the rendered page, is this project's ground
 * truth. So the isolation check fetches EACH user's own session trace via
 * RPC and confirms no other user's marker leaked into it; a real cross-talk
 * bug (two sessions aliased onto one registry entry) would surface here
 * because the aliased session's trace would then contain BOTH users' turns.
 */

export interface IsolationResult {
  ok: boolean;
  /** Markers (belonging to OTHER users) found leaked into this page/session. */
  leaked: string[];
}

/**
 * Checks `pageText` (typically `await page.locator("body").innerText()`)
 * for any of `otherMarkers` — unique per-user tokens that must never appear
 * outside their own owning session's page. Pure string function so it can be
 * unit-exercised (and reused by the negative-control test) without a live
 * browser.
 */
export function checkIsolation(pageText: string, otherMarkers: string[]): IsolationResult {
  const leaked = otherMarkers.filter((m) => m.length > 0 && pageText.includes(m));
  return { ok: leaked.length === 0, leaked };
}

/** A deterministic, sufficiently-unique per-user marker. Not a secret, just
 *  unguessable enough that accidental substring collisions across the run
 *  are effectively impossible. */
export function markerFor(runId: string, index: number, personaId: string): string {
  return `SWARM-${runId}-U${index}-${personaId}-MARKER`;
}
