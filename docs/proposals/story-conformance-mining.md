# Story coverage mining — drive a story's tests + features from real transcripts

_Status: proposed process, worked against `git-ops`. Rev 2: an adversarial review
falsified the first draft's "recipe = test oracle" premise; this revision rebuilds
on what the pipeline emits **and** folds in the bounded outcome-capture build that
makes true conformance achievable (it was wrong to wall that off as optional). See
"What changed" at the end._

> **§6 Phase 1 (outcome + intent-satisfaction capture) has LANDED** as a thin
> vertical slice (rebase → conflict → corrective-abort), no LLM, fully additive:
> new `tools/session-mining/outcomes.py`, `emit.py --outcomes`, two optional schema
> fields, fixture `tests/fixtures/intent_outcomes/`, and `tests/test_outcomes.py`
> (both it and the golden test pass). The conformance *process* (§1–§5, §7) remains
> a proposal. Remaining Phase-2 follow-ups are named in §10. One design correction
> vs the text below: the outcome join lives in **`emit.py`** (which already opens
> raw `.jsonl` + does ordinal recovery), **not** `ground.py` — see §6 items 2–3._

## The premise: inputs today, outcomes after one bounded build

The first draft claimed "the mined recipe becomes the test oracle — reality is the
expectation." That was false **against the pipeline as it stands today**: the
emitted reports carry command *inputs*, never *outcomes*. But the outcomes are not
gone — they are sitting in the raw transcripts, and recovering them is a **bounded,
proven-feasible extension** (Phase 1, §6), not a separate megaproject. So this
proposal keeps the outcome-conformance goal; it just front-loads the small enabling
build that makes it true.

**Today (inputs only).** A mined intent is a record of which commands a user drove,
in what order, with what arguments. Concretely (`analysis.schema.json:51-95`): each
action is `{tool, signature, parameters, cite, grounded}`; `grounded:true` means
"the cited trace line really contained this tool call with these args"
(`ground.py:70-90`) — *it was invoked*, never *it produced result X*. The `measured`
block is three churn counters (`tag_score.py:42-71`), not outcomes. Outcomes are
absent from the *reports* because `distill.jq:16-25` keeps only assistant/user turns
and drops every `tool_result` block.

**But the raw transcripts have everything we need** (verified on-disk, not assumed):
- Each `tool_use` block carries an `id`; the following `tool_result` carries a
  matching `tool_use_id` — a clean **1:1 join** (39 tool_uses ↔ 39 tool_results in
  a sampled session, perfectly paired).
- The result carries `is_error` (the success/fail signal) and, for Bash, a
  `toolUseResult` with `stdout` / `stderr` / `interrupted`.

So "did this `git rebase` succeed, and what did it print" **is recoverable** by
joining on `tool_use_id`. What's missing is purely that the *pipeline* discards it
at distill time — a deletion, not an absence.

| Capability | Before **Phase 1** | After **Phase 1** (outcome capture, §6 — **landed**) |
|---|---|---|
| Which workflows users drive, how often, arg vocabulary | ✅ | ✅ |
| Whether each command **succeeded** (`is_error`) | ❌ | ✅ |
| What each command **printed** (stdout/stderr head) | ❌ | ✅ |
| Whether the result **satisfied the user's intent** (the follow-up turn) | ❌ | ✅ (the strongest signal — see below) |
| **Conformance**: does the story produce the same outcome? | ❌ | ✅ (grounded — see caveat) |
| What an **oracle gate decided** (resolved diff / chosen message) | ❌ | ⚠️ partial (the gate's *result* tool-call output, not its reasoning) |

**The one caveat that survives even after Phase 1.** The transcript outcome is *raw
git* (`is_error`, stdout like `[branch abc 1a2b3c] feat: …`); the story's outcome is
*bound world* (`commits_ahead: 1`). These are different representations, so the final
"do they match" step is a **grounded human/LLM judgment**, not a string diff. Phase 1
makes that judgment *possible and evidence-backed* — it does not make conformance
fully automatic. That is the honest ceiling, and it is far above "nominate a
scenario": the author now asserts against *what really happened*, not from memory.

---

## The intent-satisfaction signal — read the follow-up turn

Mechanical success (`is_error:false`) is necessary but **not sufficient**: a
command can succeed and still be *wrong* — committed the wrong files, rebased onto
the wrong base, resolved a conflict the user didn't want. Exit-code conformance is
blind to this entire class. The ground truth for "did the outcome match the user's
**intent**" is **what the user said and did next.** Two tiers, primary is the
deterministic one:

1. **Structural (grounded, citable — preferred).** The *following* intent span's
   grounded actions are **corrective git ops on the same target**: `git reset` /
   `commit --amend` / `revert` / `rebase --abort` / `checkout -- <path>` / `stash
   drop`. That is mechanical, already-recovered evidence that the prior action was
   reworked — no sentiment classification needed. (And it composes with Phase 1: a
   corrective op that itself `is_error:false` confirms the rework actually landed.)
2. **Lexical (fuzzy fallback).** The next `user_text` — already recovered verbatim
   by `emit.py` — carries a corrective marker: "no", "undo", "revert that",
   "actually", "that's not what I meant", "instead", or a re-issued corrected
   command.

What it buys for the loop:

- A corrective follow-up means the **real outcome failed the user's intent, even on
  exit 0** — precisely the failure a flow-test's `expect_world` assertion cannot
  catch by itself.
- Two improvement shapes fall out:
  - **DIVERGES** — if the story would *reproduce the rejected outcome* (it happily
    does what the user then undid), fix the room / add the missing precondition.
  - **Gate-shaped gap (more common, and the bridge to `dev-story-mining`)** — if
    users repeatedly correct right after workflow X, X needs a **gate the story
    lacks**: a confirm, a diff-review, a base/staleness check. e.g. commit →
    immediate `--amend`/reset argues for a stronger pre-commit review gate;
    rebase → `--abort` argues for a base check before the rebase. This is exactly
    the gate-discovery signal the sibling loop hunts — the satisfaction signal is
    what connects "this story behaves wrong" to "this story is *missing a decision*."
- Secondary use for the **determinism ladder**: a gate whose recorded decisions are
  frequently followed by corrections is **not** ready to climb — its default rule is
  wrong. Correction-rate is a guardrail on L2→L3 promotion.

**Caveat — don't over-fire.** A follow-up correction is not always "previous action
wrong"; it can be legitimate iteration ("good, now also handle the test"). Treat it
as a **review flag that raises an intent's priority and nominates a gate**, not an
automatic DIVERGES. The human/LLM map adjudicates; the signal only routes attention.

---

## How this relates to `dev-story-mining` (don't oversell the reuse)

`stories/dev-story-mining/` + `.context/dev-story-from-transcripts.md` is a
**gate-discovery** loop: it compares mined gates against a story's gates —
declarative-artifact vs declarative-artifact, a like-for-like diff.

This is a **coverage** loop, and the comparison is *categorically harder*: a
command list (no outcomes) against outcome-laden room bash. They share only the
mine → map → decide → author → record *shape*; the map *question* is different
and irreducibly human. Do not expect the runnable `dev-story-mining` story to
generalize to this "for free" — see §7.

---

## Phase 0 — falsify the program before building it (cheap, mandatory)

git-ops already ships **26 flow fixtures** with dense branch-level coverage of
exactly the three git action tags (happy commit/rebase/squash, conflict
abort/auto-resolve/build-reject/escalate/second-round, pull conflict/no-tracking,
routing, staging, worktree lifecycle, merge guards). **The burden is on this
proposal to prove an uncovered gap exists.** Before writing a profile, a helper,
or a story:

1. Run the scoped mine **once** (§3).
2. Hand-classify every in-scope git intent against the rooms + the 26 fixtures.
3. Report the real **COVERAGE-GAP** and **FIXTURE-GAP** counts.

**Decision gate:** if the gap yield is near zero — the likely outcome given 26
fixtures — **shelve the program** and keep this doc as the experiment writeup.
Build the §7 tooling only if Phase 0 shows a gap worth a repeatable process.

---

## The profile — scope config, nothing more

The one reusable artifact is a small per-story profile. It is **scope
configuration**, not an auto-classifier (the first draft implied the latter;
§7 explains why a mechanical classifier is infeasible). Worked for git-ops:

```yaml
# proposed: stories/git-ops/mining.profile.yaml  (co-located ⇒ story self-describes its mine)
story: git-ops
scope:
  # prep.py --grep words. RECALL-ONLY prefilter: a substring scan over the whole
  # raw jsonl, so it over-matches prose ("merge these ideas", "commit to the design").
  # It widens the net; the real scoping is the human read in ② Map, not this.
  grep: [commit, rebase, merge, conflict, branch, worktree, stash, squash, "git pull", "git reset"]
  sample: recency          # mine how we work *now*
  max: 25
  # post-filter to these action tags. COARSE: only 3 tags exist for all git work,
  # so they cannot separate commit from pr-create/cherry-pick/force-push — the
  # non_goal boundary below is enforced by reading user_text, not by tag.
  action_tags: [commit-or-pr, rebase-or-resolve-conflicts, branch-or-worktree-setup]
# candidate rooms per action tag — a STARTING POINT for the human map, not an assertion.
owns:
  commit-or-pr:                 [commit, staging, squash, undo]
  rebase-or-resolve-conflicts:  [rebase, conflict, pull, merge_into_main, merge_branch]
  branch-or-worktree-setup:     [worktree_create, worktree_list, cleanup]
# v1 non-goals (stories/git-ops/README.md): a matching intent is OUT-OF-SCOPE, not a gap.
non_goals: [push-to-remote, pr-create, cherry-pick, bisect, "rebase -i", force-push, submodules]
```

Adding a story to the program = adding a profile. Nothing in the pipeline reads
it yet; it is a convention + an input to the §7 data-prep helper.

---

## The loop

Two of the five verdicts (**MATCH-coverage**, **COVERAGE-GAP**) need only inputs
and work today. The two that serve the literal goal (**CONFORMS**, **DIVERGES**)
need Phase 1's outcome capture (§6). The loop is written for the post-Phase-1
world and flags what degrades without it.

### ① Mine — scoped (deterministic except the one oracle pass)

```sh
cd tools/session-mining
JOB=gitops-coverage-$(date +%Y%m%d); PROJ=~/.claude/projects/<slug>; JOBDIR=.artifacts/session-mining/$JOB

# A: distill+batch, scoped by the profile's grep/sample/max (recall-only prefilter)
python3 prep.py "$PROJ" --job "$JOB" --sample recency --max 25 \
  --grep commit --grep rebase --grep merge --grep conflict \
  --grep branch --grep worktree --grep stash --grep squash --grep "git pull"
# B: the one LLM step — intents.workflow.js over $JOBDIR/batches (schema-gated)
# C–F deterministic: ground.py → tag_score.py → emit.py
python3 verify_link.py "$JOBDIR" && python3 validate_reports.py "$JOBDIR"

# scope post-filter to the profile's action tags ⇒ focused intent set
jq '[.intents[] | select(.tags.action[] | IN("commit-or-pr","rebase-or-resolve-conflicts","branch-or-worktree-setup"))]' \
  "$JOBDIR/intents.json" > "$JOBDIR/intents.git.json"
```

Caveat carried forward from the honest premise: the resulting recipes are command
lists. They tell you the **scenario and its command vocabulary**, not its result.

### ② Map — a human reads the room bash against the recipe

This step is **not mechanical** and the first draft was wrong to call it so.
git-ops git commands live inside multi-line `bash -c` heredocs with in-script
control flow — `rebase.yaml:36-70` is one `host.run` wrapping `git tag`, `git
rebase`, `git merge-base`, an `if [ $EXIT -eq 0 ]` branch, and two `jq -n` route
constructions; `conflict.yaml` packs rebase-continue, the go.sum special-case,
build-check, and abort into one room behind `grep -qi "CONFLICT"` branches. There
is **no `host.run`-per-command effect chain** to diff a mined `git rebase main`
against. Recognising that a mined `git checkout --theirs go.sum && go mod tidy`
"maps to" the `gosum_fix` branch (`conflict.yaml:119-131`) requires reading shell,
not aligning a sequence. A human (or an LLM in the runnable variant) does the map;
the recipe is **evidence**, not the oracle.

For each in-scope intent, assign one verdict, reading three lenses: **input-driven**
(which room/fixture covers this workflow?), **outcome-driven** (does the story
produce what really happened? — Phase 1 `outcome`), and **intent-driven** (did the
real result satisfy the user, or did the follow-up correct it? — Phase 1
`satisfaction`). A `corrected:true` intent is the loudest pointer to a real
problem even when the command itself succeeded:

| Verdict | Test | Needs outcomes? | Action |
|---|---|---|---|
| **CONFORMS** | a room reaches this workflow, a fixture exercises the branch, **and** the fixture's asserted world reflects the transcript's real outcome | yes (Phase 1) | leave |
| **DIVERGES** | a room reaches it but produces a **different** outcome than the transcript (e.g. transcript's `git merge` succeeded; the room's guard would have blocked it) | yes (Phase 1) | fix the room **or** declare the transcript suboptimal and pin the *refined* outcome; update the fixture |
| **FIXTURE-GAP** | a room models the workflow but no fixture exercises **this** branch/variant | no | author a fixture (no story change) |
| **COVERAGE-GAP** | no room reaches this command shape, and it is not a `non_goal` | no | new room + fixtures (the only verdict that adds a *feature*) |
| **OUT-OF-SCOPE** | matches a `non_goal` (read `user_text`; tags can't tell) | no | note, drop |

Without Phase 1, CONFORMS/DIVERGES collapse into a single "a room reaches it"
(call it MATCH-coverage) — you learn the workflow is *modelled*, not whether it
behaves *correctly*. That collapse is exactly the gap Phase 1 closes, and exactly
the user's stated goal, so Phase 1 is in-scope, not optional (§6).

The recipe is **evidence**, not an auto-classifier: a human (or an LLM in the
runnable variant) reads the room bash and, post-Phase-1, the recorded outcome.

### ③ Decide — rank and ticket

Rank FIXTURE-GAP / COVERAGE-GAP by **frequency × mechanicalness**. Frequency is
the honest signal the mine is good at — but note `clusters` key on tool-sequence
only (`Bash>Bash`; `tag_score.py:113-121`), so every git intent collapses into one
bucket and gives **no dedup**. Dedup by command-shape (arg-aware) is a manual or
small-helper step (§7); the ⑤ metric's denominator depends on it. Write a ticket
naming the exact room/flow file and the verdict.

### ④ Author — a human authors the fixture; the transcript informs the expectation

A git-ops fixture **is** its outcomes: `happy_path_commit.yaml:15-91` stubs
`refresh_branch` / `classify_staging` / `diff_stat` / `git_commit` with full
`stdout_json` payloads and asserts `expect_world` on the bound results. The author
still *writes* the stub + assertion (the engine renders them), but **Phase 1
grounds them in reality**: the recorded `is_error` + stdout for each real command
tells the author what the stub should return and what world the run produced, so
the assertion reflects what actually happened rather than the author's memory. So:

- **DIVERGES** → fix the room, then author/update the fixture to the canonical
  outcome (transcript or refined); the recorded transcript outcome is the evidence
  the fix is correct.
- **FIXTURE-GAP** → new `flows/*.yaml`; the recorded command sequence + outcomes
  seed the stub responses and the asserted state-path + world.
- **COVERAGE-GAP** → new room + its fixtures.
- **Oracle-gated workflows** (commit-message, conflict-resolution; `determinism:
  oracle-gated`): Phase 1 recovers the gate's *result* (the `submitted` tool-call
  output — the chosen message, the `resolved` verdict) but not its reasoning, which
  is enough to stub `conflict_verdict` / the commit message from the real run
  rather than inventing one.

The **expectation is always author-owned but evidence-backed**. That cleanly
absorbs the "refine beyond transcript basis" clause: when the real run was messy,
the author pins the *intended* outcome and the worksheet flags the transcript as
improved-upon; when it was clean, the author pins it as-observed. Every change
ships a green `kitsoki test flows git-ops`, no live LLM in fixtures (stub the
gates, per AGENTS.md).

### ⑤ Record — an honest, manually-denominated metric

Track **# in-scope git intents that CONFORM** (room reached + fixture exercises the
branch + asserted world matches the real outcome) out of the **deduped** in-scope
total. Re-mine after a burst of git-ops dogfooding; DIVERGES / FIXTURE-GAP /
COVERAGE-GAP should fall as you close them. Where a gate's recovered results are
dominated by one branch (e.g. `conflict_resolver` almost always `resolved:true` on
go.sum-only), that is a determinism-ladder candidate (L2→L3) — Phase 1's recovered
gate *results* are exactly the labels that move drives.

---

## Worksheet skeleton (`coverage.md`)

Filled by ②. Illustrative rows — **real verdicts come from the actual mine.** The
`real outcome` column is populated by Phase 1; without it those cells are blank and
verdicts degrade to MATCH-coverage.

| # | intent (trunc.) | recipe (commands) | real outcome (Phase 1) | candidate room | verdict | note |
|--:|---|---|---|---|---|---|
| 1 | "commit these fixes" | `git status` → `checkout -b x` → `git commit -q -F -` | `is_error:false`, `[x 1a2b3c] feat: …` | `commit` / `staging` | CONFORMS | `happy_path_commit` asserts `commits_ahead:1` ✓ |
| 2 | "rebase onto main and resolve" | `git rebase main` → edits → `--continue` | `is_error:true` then `false` (conflict→resolved) | `rebase`→`conflict` | FIXTURE-GAP? | does a fixture cover *this* file-set? |
| 3 | "merge feature into main" | `git merge --no-ff feat` | `is_error:false` (merged) | `merge_into_main` | DIVERGES? | room's descendant guard would *block* this — story stricter than reality; intended? |
| 4 | "commit the staged work" | `git commit -m …` → **next turn:** `git reset --soft HEAD~ && git commit --amend` | `is_error:false` but **`corrected:true`** | `commit` | gate-gap | succeeded yet immediately reworked ⇒ argues for a pre-commit review gate the story lacks |
| 5 | "force push the rebase" | `git push --force-with-lease` | `is_error:false` | — | OUT-OF-SCOPE | `non_goal`; read user_text |

Summary line: `N deduped · X CONFORMS · D DIVERGES · C corrected(satisfaction) · Y FIXTURE-GAP · Z COVERAGE-GAP · O out-of-scope`.

---

## §6 Phase 1 — outcome capture (the in-scope enabling build)

This is the build that makes "verify real outcomes match the transcript" true. It
is **bounded and proven feasible** — the data already exists in the raw transcripts
(verified on-disk, above); the pipeline just discards it. Four changes, each small
and backward-compatible (every field is *additive* — existing reports and
`dev-story-mining` keep working):

1. **Recover outcomes from raw (new `outcomes.py`, ~1 stdlib script).** Read each
   session's raw `.jsonl`, join `tool_use.id → tool_result.tool_use_id` (1:1), and
   emit a per-session **ordered list** of `{is_error, stdout_head, stderr_head,
   interrupted}` — one entry per tool call, in trace order. Mirrors the ordinal
   recovery `emit.py` already does for user-text. No LLM, deterministic. *(A genuinely
   missing mid-stream result — interrupted/abandoned call — becomes a `null` entry
   rather than cascading a later call's result onto it.)*
2. **Join outcomes onto grounded actions (in `emit.py` via `--outcomes`).** Each
   action's `cite.line` already pins it to a trace line; count the `> ` tool lines up
   to that line to get the **tool-call ordinal**, index into (1)'s list, attach as
   `action.outcome`. `emit.py` is the right home (not `ground.py`): it already opens
   raw `.jsonl` and does ordinal recovery, so it avoids growing a raw dependency in
   the grounding step (DI / least-surprise).
3. **Capture the intent-satisfaction signal (same `emit.py` pass).** Per intent span,
   record the **next user turn's text** (the lexical tier — `emit.py` already locates
   user turns by ordinal) and a derived `corrected:bool` flag from the
   **immediately-following span's grounded actions** matching the corrective-op set
   (`reset`/`amend`/`revert`/`rebase --abort`/`checkout --`/`stash drop`) — the
   structural tier. Emitted per instance as `satisfaction:{followup_text_head,
   corrected, corrective_ops:[…]}`. It is a recall-biased **review flag** (following
   span only, no target-overlap check), not an auto-verdict.
4. **Schema (`analysis.schema.json`).** Added optional `outcome:{is_error,
   stdout_head, stderr_head, interrupted}` to the action object and optional
   `satisfaction:{…}` to the instance (both `additionalProperties:false`, neither
   `required`). Optional ⇒ no break to existing consumers.
5. **(Deferred — see §10) sharpen determinism (`tag_score.py`).** A span whose commands all
   `is_error:false`, no retries, **and `corrected:false`** is stronger evidence of
   `deterministic`; failed-then-fixed or corrected-after is evidence of
   `oracle-gated`. Nice-to-have, not required.

The **distill trace stays byte-for-byte unchanged**, so the oracle's line
citations (and every existing test fixture in `tools/session-mining/tests/`) are
unaffected — outcomes are joined *out-of-band* by ordinal, not by editing the
trace the LLM sees. That is what keeps this bounded rather than a pipeline rewrite.

Cost (as landed): one new ~130-line script (`outcomes.py`) + ~40 lines in `emit.py`
+ two optional schema fields, plus a fixture (`tests/fixtures/intent_outcomes/`) and
test (`tests/test_outcomes.py`). A focused half-day, not a project — shipped as a
thin vertical slice (one workflow: rebase → conflict → corrective-abort) so the
outcome-conformance chain is proven end-to-end before generalising. (The
satisfaction flag ended up in `emit.py`, not a shared raw pass — see items 2–3.)

What Phase 1 still does **not** buy (the §-premise caveat): the recorded outcome is
raw git; the story's outcome is bound world. Matching them is a grounded human/LLM
judgment, not a string compare. Phase 1 makes the judgment *evidence-backed*; it
does not eliminate it.

---

## §7 Packaging — honest tiers

1. **Doc + profile (this file + `mining.profile.yaml`)** — the minimum. Run the
   §3 recipe, fill `coverage.md` by hand.
2. **Phase 1 outcome capture (§6)** — the enabling build for CONFORMS/DIVERGES.
   In-scope because it *is* the user's goal; bounded to a half-day vertical slice.
3. **A *data-prep* helper** — `tools/session-mining/coverage_prep.py` taking
   `$JOBDIR` + a profile and emitting: `intents.git.json` (scope-filtered), an
   **arg-aware command-shape dedup** (fixing the tool-sequence-only clusters), a
   **frequency ranking**, and a `coverage.md` skeleton with each intent pre-joined
   to candidate rooms by keyword **and its Phase-1 outcomes inlined**. Mechanical
   data prep only — it does **not** assign verdicts (that's §4's human/LLM read of
   room bash vs outcome). Feasible; the first draft's "auto-fill every row except
   the verdict" was not.
4. **A runnable `story-mining` story** — generalize `dev-story-mining` with the map
   gate switched to the conformance verdict, judge-polymorphic. Inherits §4's
   human/LLM-in-the-loop map (no "for free"). Highest investment; justified only if
   this becomes a recurring, multi-story program.

---

## Recommendation

1. **Phase 0 first (cheap, decides everything).** Run one scoped mine and
   hand-classify the git intents at the *coverage* level (CONFORMS-collapsed-to-
   MATCH / FIXTURE-GAP / COVERAGE-GAP). Coverage classification needs no outcomes,
   so this is free and tells you whether there's any gap worth a program against the
   26 existing fixtures.
2. **In parallel, do the Phase 1 vertical slice (§6) for one workflow.** Half a day;
   proves the outcome-conformance chain end-to-end and de-risks the "is it really
   bounded" question with running code rather than a doc's promise.
3. **Then decide the program's size from real numbers** — if coverage gaps are near
   zero but conformance reveals real DIVERGES, the value is in Phase 1 + tier 3; if
   coverage gaps are large, fix those first (no outcomes needed). Ship tiers 1–3 and
   the runnable story only if the gap and the appetite are both real.

---

## What changed from the first draft (adversarial-review fixes)

- **Cut "the recipe is the test oracle / reality is the expectation" *as a
  property of today's pipeline*.** The emitted reports carry command *inputs*, never
  outcomes (`distill.jq` drops tool_results; `measured` is 3 counters; `grounded` =
  "invoked"). Assertions are author-owned — but now **evidence-backed** by Phase 1's
  recovered outcomes, not just memory.
- **Outcome-based DIVERGENCE: restored, and the enabling build brought in-scope.**
  Rev 1 walled outcome capture off as an optional "Foundation track" — that was an
  overcorrection. Verified on-disk that raw transcripts carry `is_error` + stdout
  joined 1:1 by `tool_use_id`, so capture is a **bounded ~half-day additive build**
  (§6 Phase 1), not a separate project. DIVERGES/CONFORMS are first-class verdicts
  again, gated on that build with a thin vertical slice to de-risk it.
- **Added the intent-satisfaction signal (the follow-up turn).** Exit-code
  conformance is blind to "succeeded but wrong"; the user's *next* turn is ground
  truth for intent-match. Captured in the same Phase 1 raw pass — structurally
  (following span's corrective git ops: reset/amend/revert/abort) and lexically
  (next `user_text`). It routes a corrected-after intent to either DIVERGES or, more
  often, a **gate-shaped gap** — which is the bridge to `dev-story-mining`'s
  gate-discovery loop — and guards determinism-ladder promotion.
- **Cut "mechanical diff" and the auto-classifier helper.** git-ops git commands
  live in branching bash heredocs; the map is a human read. The helper is demoted
  to honest *data prep* (scope-filter, arg-aware dedup, frequency, candidate-room
  join) that assigns no verdicts.
- **Added Phase 0** — prove the gap exists against 26 existing fixtures before
  building anything.
- **Made scoping honesty explicit** — `--grep` is a recall-only prefilter that
  over-matches prose; the 3 action tags are too coarse to separate non_goals; the
  real scope is the human read of `user_text`.
- **Named the completeness holes** — oracle-gated intents carry no resolved
  artifact; tool-sequence clusters give no dedup; multi-tag intents break
  one-room ownership; `mining.profile.yaml` is proposed, not extant.
- **Softened the `dev-story-mining` sibling framing** — shared skeleton, but a
  categorically harder map step; no "for free" generalization.

---

## §10 Phase-2 follow-ups (named, deferred from the §6 slice)

The §6 slice deliberately shipped the minimum that proves the chain end-to-end.
These are the named extensions, each independent:

1. **Determinism sharpening in `tag_score.py`** (§6 item 5). A span with all
   `is_error:false`, no retries, **and `corrected:false`** is stronger evidence of
   `deterministic`. Needs outcomes available at *scoring* time — so reorder the
   pipeline to run `outcomes.py` before `tag_score.py` and have both read
   `outcomes.json`. Deferred because step E runs *before* emit today.
2. **Target-overlap precision on corrective detection.** The satisfaction flag is
   currently recall-biased (any corrective git op in the following span trips it).
   Intersect the corrective op's path/symbol with the original action's target to
   cut false positives before promoting it past a review flag.
3. **Multi-span lookahead.** Detect corrections beyond the *immediately* following
   span (a user who corrects two turns later).
4. **`coverage_prep.py` (§7 tier 3)** inlining the Phase-1 outcomes into the
   `coverage.md` worksheet — separate, mechanical data-prep change.
