<!-- WORKED worksheet — the coverage_prep.py skeleton with the VERDICT + NOTE
     columns filled by the human/LLM map step (reading each room's bash against
     the recovered outcome). This is the committed reference answer for the
     git-ops flagship; `run.sh` regenerates the blank skeleton next to it.
     See docs/stories/story-coverage-mining.md for the loop this completes. -->

# Coverage worksheet (worked) — `git-ops`

_7 in-scope intents · 7 deduped command-shapes · 0 out-of-scope by tag (the
force-push intent is in-scope *by tag* but OUT-OF-SCOPE by `user_text` — exactly
the case the tags are too coarse to separate, caught by the non_goal hint)._

The **real outcome** and **satisfaction** columns come from Phase 1 (the recovered
`outcome` + `satisfaction` fields). The **Verdict** is assigned by reading the
candidate room's bash against that outcome — the irreducibly human/LLM step.

| # | intent (user_text) | command shape | real outcome (Phase 1) | satisfaction | candidate rooms | Verdict | Note |
|--:|---|---|---|---|---|---|---|
| 1 | commit the staged fix | `status --short → commit -m <msg>` | both ✓ | — | commit, staging, squash, undo | **CONFORMS** | `commit` room reaches it; `happy_path_commit.yaml` exercises staging→commit and asserts `commits_ahead:1`. Asserted world reflects the real ok outcome. |
| 2 | commit the staged work | `commit -m <msg>` | ✓ (`[feat-cache 9f8e7d6] wip…`) | ⚠ corrected: reset --soft, restore --staged, commit --amend | commit, staging, squash, undo | **DIVERGES (gate-gap)** | Succeeded yet immediately reworked: the commit pulled in a *generated* file (`mocks_gen.go`). `staging` classifies only credentials/binaries as "suspicious" — a generated file slips through, and `commit` reviews the *message*, not the staged file-set. Argues for a missing gate: a generated-file class in `staging`, or a staged-file review before commit. The loudest pointer here. |
| 3 | (amend it without the generated mocks) | `reset --soft <ref> → restore --staged <file> → commit --amend -m <msg>` | all ✓ | — | commit, staging, squash, undo | **COVERAGE-GAP** | The manual workaround for #2. `undo` models whole-commit `reset --soft/--mixed/--hard`, but no room models *selective* `git restore --staged <file>` + `git commit --amend`. Closing #2's gate would remove the need for this; absent that, it's an unmodelled corrective shape. |
| 4 | rebase onto main and resolve the conflicts | `rebase <branch> → edit <file> → edit <file> → rebase --continue` | rebase **✗ CONFLICT**, continue ✓ (`Successfully rebased`) | — | rebase, conflict, pull, merge_into_main, merge_branch | **FIXTURE-GAP** | `rebase`→`conflict` model multi-file agent resolution, but every conflict fixture covers a *single* file (`conflict_auto_resolved` → `internal/foo.go`) or the go.sum special-case. No fixture exercises a **multi-file** (2× app-code) auto-resolve + `--continue`. Author a `flows/conflict_multifile_resolved.yaml`; the recovered outcome seeds the stub. No story change. |
| 5 | merge the feature branch into main | `merge --no-ff <branch>` | ✓ (`Merge made by 'ort'`) | — | rebase, conflict, pull, merge_into_main, merge_branch | **DIVERGES** | The transcript merged directly and it worked. `merge_into_main` Guard 1 (descendant + stale-`rebase_base_sha` check) would emit `re_rebase_needed` and **never attempt** the merge unless a prior story-driven rebase recorded the base (`merge_descendant_guard.yaml` asserts exactly this). Story is *stricter* than reality. Decide: is the rebase-before-merge guard intended (transcript suboptimal → pin the refined outcome) or too strict (relax the guard)? |
| 6 | set up a worktree for the new cache feature | `worktree add <path> -b <branch>` | ✓ (`Preparing worktree (new branch …)`) | — | worktree_create, worktree_list, branch_ops, cleanup | **CONFORMS** | `worktree_create` reaches it; `happy_path_worktree_lifecycle.yaml` exercises create→…→cleanup. Real ok outcome matches. |
| 7 | force push the rebased branch to origin | `push --force-with-lease <remote> <branch>` | ✓ | — | (commit, staging, squash, undo) | **OUT-OF-SCOPE** | `force-push` and remote push are v1 non-goals (`README.md` "Non-goals (v1)"). Tagged `commit-or-pr` (the tags can't separate it) but the `user_text` + non_goal hint (`⚑ force push`) place it out of scope. Note and drop. |

**Summary line:** `7 deduped · 2 CONFORMS · 2 DIVERGES (1 gate-gap) · 1 corrected · 1 FIXTURE-GAP · 1 COVERAGE-GAP · 1 out-of-scope`.

## What this worked example demonstrates

- **Outcome-driven verdicts (the user's goal).** Rows 4 and 5 are decided by *what
  really happened* (`is_error`, the merge output), not by guessing — the property
  that did not exist before Phase 1.
- **Intent-driven verdict (the strongest signal).** Row 2 *succeeded on exit 0* yet
  is the most actionable finding, surfaced only by `satisfaction.corrected` reading
  the follow-up turn. It routes to a **gate-shaped gap** — the gate-discovery signal
  (see `docs/stories/story-coverage-mining.md`).
- **The honest ceiling.** The recovered outcome is raw git (`Merge made by 'ort'`);
  the story's outcome is bound world (`last_op_outcome: merged`). Matching them (row
  5) is a grounded human judgment, not a string compare — Phase 1 makes it
  *evidence-backed*, not automatic.
- **Coarse tags need the human read.** Row 7 is in-scope by tag, out-of-scope by
  intent. The non_goal hint routes attention; the verdict is still a human call.

## Actions ranked (frequency × mechanicalness)

In this small corpus every shape appears once, so rank by impact:

1. **Row 2 — pre-commit gate-gap** (DIVERGES/gate-gap). Highest value: a recurring
   succeeded-but-reworked signal is the canonical "story is missing a decision."
2. **Row 5 — merge guard divergence** (DIVERGES). Decide intended-strictness, then
   pin the fixture to the canonical outcome.
3. **Row 4 — multi-file conflict fixture** (FIXTURE-GAP). Pure test add, no story
   change; the recovered outcome seeds the stub.
4. **Row 3 — selective-unstage + amend** (COVERAGE-GAP). Likely subsumed by closing
   row 2's gate; revisit after.
