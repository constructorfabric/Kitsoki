/**
 * Display helpers for intent names.
 *
 * Intent names carry import-alias plumbing (`core__prd__start`) and snake_case
 * slugs (`submit_answers`) — fine as identifiers, ugly on screen. `humanizeIntent`
 * turns them into something a person reads: drop the alias prefix (everything up
 * to the last `__`) and turn underscores into spaces, so `core__prd__start` →
 * `start` and `core__prd__submit_answers` → `submit answers`.
 *
 * This is DISPLAY ONLY — the raw intent name stays the source of truth in the
 * trace and the data layer; surfaces that show a label humanise it (and keep the
 * raw name available on hover) so demos and the operator UI never read like a
 * machine. Prefer an authored `title` when present; fall back to this.
 */
export function humanizeIntent(name: string | undefined): string {
  if (!name) return "";
  return (name.split("__").pop() ?? name).replace(/_/g, " ");
}
