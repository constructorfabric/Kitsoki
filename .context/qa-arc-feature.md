# Slidey hybrid demo — the developer arc (dev-story on the slidey repo)

## What this is

A self-contained slidey HTML deck (`.artifacts/slidey-hybrid/dev-story-arc.html`)
that tells the **developer half** of a dev-story, end to end, on the **slidey**
repo. It interleaves narrated slides with **three native, autoplaying rrweb
kitsoki tour embeds** — each tour is a deterministic, no-LLM replay of the real
kitsoki web UI driving a real slidey scenario.

The deck is the deliverable. What a viewer should see, in order:

1. A **title** slide: "Fix a bug, refine a feature, open the PR".
2. A narrative slide framing **the bug**, then a tour video where kitsoki's
   **bug-fix pipeline** reproduces, proposes a fix, implements it in an isolated
   worktree, runs the real test suite, self-reviews, and validates a real slidey
   bug (grid-card decks drift their narration past six cards). The tour shows the
   kitsoki trace/chat UI advancing through reproduce → propose → implement →
   test → review → validate, with a tour popover narrating each step.
3. A narrative slide framing **the refine**, then a tour video where a reviewer
   points at an exact element on a rendered deck frame (a semantic anchor) and an
   **anchored refine pass** edits precisely the scene pointed at — the rendered
   deck poster with a blue annotation overlay, then a re-render.
4. A narrative slide framing **the ship**, then a tour video where the fix
   **opens a pull request** (PR #128 on the slidey repo), CI goes green, and the
   merge lands.
5. A closing **cta** slide: every frame is a deterministic, no-LLM replay.

## What "works" means (the claims to verify)

- Each of the three embedded tours **plays its content** — it must NOT sit frozen
  on its first frame (the home/library view). The tour should visibly advance
  (playhead moving, tour popover stepping, the kitsoki UI changing state).
- The bug-fix tour shows the kitsoki bug-fix pipeline UI (trace rows / state
  for reproduce→fix→validate) for a slidey bug.
- The refine tour shows the rendered deck poster + an annotation overlay and an
  anchored refine.
- The PR tour shows a pull request being opened (PR id / url), CI, and a merge.
- The deck is a coherent "developer arc": title → 3 narrated phases each paired
  with its tour → cta. No missing/blank scenes; narration overlays are readable.

## Why it matters

This proves the hybrid-demo format (narrated slides + native rrweb tour embeds)
works with real per-phase content, and that the tour embeds autoplay hands-free
(a slidey fix landed for exactly this).
