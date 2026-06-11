/**
 * Local flag model for the /review feedback panel. A Flag is an in-browser
 * annotation pinned to a moment (a point or a [start,end] range) of the video:
 * it resolves to the dominant chapter's source_ref, carries a captured still
 * handle, and accumulates an instruction the operator dispatches as a
 * structured feedback note. Flags live only in the page (the server is the
 * source of truth once dispatched); they are NOT persisted client-side.
 */
import type { Chapter, SourceRef } from "../data/source.js";

export interface Flag {
  /** Stable local id (monotonic). */
  id: number;
  /** Flagged window; end === start for a point flag. */
  start_ms: number;
  end_ms: number;
  /** Resolved dominant chapter (max time overlap), if any. */
  chapter: Chapter | null;
  /** Captured still artifact handle (from runstatus.video.frame). */
  frame_handle: string | null;
  /** The operator's instruction (the per-flag chat's outgoing note). */
  instruction: string;
  /** Whether this flag has been dispatched to refine. */
  sent: boolean;
}

/**
 * dominantChapter is the SINGLE chapter resolver for a flag — both the
 * Flags-list label and the FlagDetail source_ref derive from its result (they
 * read the same resolved `flag.chapter`), so they can never disagree for the
 * same flag.
 *
 * It picks the chapter whose [start,end) window has the greatest overlap with
 * [start_ms, end_ms] (proposal open-question 2: dominant chapter in v1). A
 * point flag (start === end) picks the chapter whose window CONTAINS it. Only
 * when the flag sits in a gap or past the end (no containing window) does it
 * fall back to the nearest preceding chapter.
 *
 * Pre-fix the producer could emit zero-width windows (all [0,0]); the
 * containment test then failed for every flag and the nearest-preceding
 * fallback returned scene-0 regardless of timestamp — the still showed the
 * real slide while the label read scene-0. The producer now always emits real,
 * contiguous windows; this resolver stays exact as defense in depth.
 */
export function dominantChapter(
  chapters: Chapter[],
  start_ms: number,
  end_ms: number
): Chapter | null {
  if (chapters.length === 0) return null;
  const lo = Math.min(start_ms, end_ms);
  const hi = Math.max(start_ms, end_ms);

  // A point flag resolves to the window that contains it — exact, never the
  // nearest-preceding approximation.
  if (hi === lo) {
    const containing = chapters.find((c) => c.start_ms <= lo && lo < c.end_ms);
    if (containing) return containing;
  }

  let best: Chapter | null = null;
  let bestOverlap = -1;
  for (const c of chapters) {
    const overlap = Math.max(
      0,
      Math.min(hi, c.end_ms) - Math.max(lo, c.start_ms)
    );
    if (overlap > bestOverlap) {
      bestOverlap = overlap;
      best = c;
    }
  }
  // No positive overlap (the flag sits in a gap / past the end): fall back to
  // the nearest preceding chapter by start_ms.
  if (bestOverlap <= 0) {
    best = chapters
      .filter((c) => c.start_ms <= lo)
      .sort((a, b) => b.start_ms - a.start_ms)[0] ?? chapters[0];
  }
  return best;
}

/** sourceRefFor returns the resolved chapter's source_ref, or undefined. */
export function sourceRefFor(flag: Flag): SourceRef | undefined {
  return flag.chapter?.source_ref;
}

/** Format milliseconds as M:SS for labels. */
export function formatMs(ms: number): string {
  const total = Math.floor(ms / 1000);
  const m = Math.floor(total / 60);
  const s = total % 60;
  return `${m}:${s.toString().padStart(2, "0")}`;
}

/**
 * vscodeLink builds the `vscode://file/{path}:{line}` deep-link for a
 * source_ref, matching the story-editor's IDE-link pattern. Returns null when
 * there is no spec path to point at.
 */
export function vscodeLink(ref: SourceRef | undefined): string | null {
  if (!ref || !ref.spec_path) return null;
  const line = ref.line && ref.line > 0 ? `:${ref.line}` : "";
  return `vscode://file/${ref.spec_path}${line}`;
}
