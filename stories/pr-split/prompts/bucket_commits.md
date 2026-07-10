Group a branch's commits into pull-request buckets by concern.

**First, read the briefing file `.git/kitsoki-prsplit-input.txt`** (relative to
the repo root / your working directory) with the Read tool. It contains the
source and integration branches, the commits (oldest first) with a deterministic
concern guess each, and the changed files per commit. That file is the source of
truth — do NOT assume the input is empty; read it.

Then group the commits:

- Every sha in the briefing must appear in exactly ONE bucket. Never split or
  drop a commit.
- Keep each commit's deterministic concern unless it spans multiple concerns, in
  which case place it in the single most appropriate bucket so each PR stays
  coherent.
- Within a bucket, keep commits in their original (oldest-first) order so a
  cherry-pick applies cleanly.
- One bucket per concern. Prefer fewer, coherent PRs over many tiny ones, but do
  not mix a core code change with unrelated tooling or docs.
- Write a clear PR `title` and a `body` (markdown) for each bucket summarizing
  what it changes and why it is independently reviewable.

Return the buckets via the submit tool against the pr_buckets schema. Use the
real commit shas from the briefing (the full 40-char sha is fine; the 8-char
prefix shown is also acceptable).
