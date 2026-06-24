# Story coverage mining — drive a story's tests + features from real transcripts

Story coverage mining points the [`session-mining`](../../tools/session-mining/README.md)
intent pipeline at a kitsoki **story** and asks: of the workflows people actually
drive in real Claude Code sessions, which ones does the story model correctly,
which does it get *wrong*, and which is it *missing*? It turns recorded reality
into a prioritised list of fixtures to add and rooms to fix.

The worked, runnable answer is the **git-ops flagship**:
[`tools/session-mining/examples/git-ops/`](../../tools/session-mining/examples/git-ops/)
— a committed corpus, a one-command demo (`run.sh`), and a filled worksheet
([`coverage.worked.md`](../../tools/session-mining/examples/git-ops/coverage.worked.md)).
Read this doc for the loop; read the flagship for a concrete instance of it.

## What the pipeline gives you (inputs **and** outcomes)

A mined intent is, first, a record of *which commands a user drove, in what order,
with what arguments* — the grounded `actions` of `analysis.json`. `grounded:true`
means "the cited trace line really contained this tool call with these args"
(it was *invoked*), not "it produced result X".

The **outcomes** are recovered too, by the Phase-1 extension (`outcomes.py` +
`emit.py --outcomes`): each action carries the real `outcome`
(`{is_error, stdout_head, stderr_head, interrupted}`) joined from the raw
transcript by tool ordinal, and each instance carries a `satisfaction` review
flag. See the [session-mining README](../../tools/session-mining/README.md#intent-mining-the-third-mode)
(step E′/F) for the mechanism; it is not repeated here.

That gives three lenses on every intent:

- **input-driven** — which room/fixture covers this workflow? (coverage)
- **outcome-driven** — did the command succeed, and what did it print? (`outcome`)
- **intent-driven** — did the result satisfy the user, or did the follow-up
  turn *correct* it? (`satisfaction`)

**The honest ceiling.** The recovered outcome is *raw git* (`is_error`, stdout like
`Merge made by the 'ort' strategy`); a story's outcome is *bound world*
(`last_op_outcome: merged`). These are different representations, so the final
"do they match" is a **grounded human/LLM judgment**, not a string diff. Phase 1
makes that judgment *evidence-backed* — the author asserts against what really
happened, not from memory — it does not make conformance automatic.

## The intent-satisfaction signal — read the follow-up turn

Mechanical success (`is_error:false`) is necessary but **not sufficient**: a
command can succeed and still be *wrong* — committed the wrong files, rebased onto
the wrong base. The ground truth for "did the outcome match the user's intent" is
**what the user said and did next**. `satisfaction` captures it in two tiers:

1. **Structural (grounded, preferred)** — the immediately-following intent span
   contains a *grounded corrective git op* (`reset` / `commit --amend` / `revert` /
   `rebase --abort` / `checkout --` / `stash drop`). Mechanical, already-recovered
   evidence that the prior action was reworked.
2. **Lexical (fallback)** — the next `user_text` carries a corrective marker
   ("no", "undo", "actually", "instead", a re-issued command).

A corrected-after intent means the real outcome **failed the user's intent even on
exit 0**. It routes to one of two improvement shapes:

- **DIVERGES** — the story would *reproduce the rejected outcome*; fix the room.
- **Gate-shaped gap (more common)** — users repeatedly correct right after workflow
  X ⇒ X needs a **gate the story lacks** (a confirm, a diff-review, a base check).
  This is the **gate-discovery** signal: the satisfaction flag is what connects "this
  story behaves wrong" to "this story is *missing a decision*" (the deferred
  `story-mining` story in *What remains* below would automate this map).

It is a recall-biased **review flag** that raises an intent's priority and nominates
a gate — never an automatic verdict (it inspects only the *immediately* following
span and does not verify target overlap). The human/LLM map adjudicates.

## The loop

### ① Mine — scoped

Run the [intent-mining pipeline](../../tools/session-mining/README.md#running-it)
(`prep.py → intents.workflow.js → ground → tag_score → outcomes → emit --outcomes`)
scoped by the story's **profile** (below), then `coverage_prep.py` to prepare the
worksheet. The result is a command list per intent plus its recovered outcome and
satisfaction flag.

### ② Map — a human/LLM reads the room bash against the recipe

**Not mechanical.** A story's git commands live inside branching `bash -c` heredocs
with in-script control flow (e.g. `stories/git-ops/rooms/conflict.yaml` packs
rebase-continue, the go.sum special-case, build-check, and abort into one room
behind `grep -qi "CONFLICT"` branches). Recognising that a mined
`git checkout --theirs go.sum && go mod tidy` "maps to" the gosum branch requires
reading shell, not aligning a sequence. The recipe + outcome is **evidence**; a
human (or an LLM) assigns the verdict.

A note on the two words: **coverage mining** is the activity (the whole
mine → map → author loop); the per-intent judgment it produces is the
**conformance verdict** (one of the five below). "Coverage" is the headline
name across the skill, tooling, and worksheets; "conformance" is reserved for
the verdict — they are not two features.

For each in-scope intent, one verdict:

| Verdict | Test | Action |
|---|---|---|
| **CONFORMS** | a room reaches it, a fixture exercises the branch, and the asserted world reflects the transcript's real outcome | leave |
| **DIVERGES** | a room reaches it but produces a **different** outcome than the transcript (incl. a guard that would block what really worked, or a succeeded-but-corrected gate-gap) | fix the room **or** declare the transcript suboptimal and pin the *refined* outcome; update the fixture |
| **FIXTURE-GAP** | a room models the workflow but no fixture exercises **this** branch/variant | author a fixture (no story change) |
| **COVERAGE-GAP** | no room reaches this command shape, and it is not a non-goal | new room + fixtures (the only verdict that adds a *feature*) |
| **OUT-OF-SCOPE** | matches a story non-goal (read `user_text`; tags are too coarse) | note, drop |

### ③ Decide — rank and ticket

Rank FIXTURE-GAP / COVERAGE-GAP by **frequency × mechanicalness**. `coverage_prep.py`
supplies the arg-aware frequency (its dedup fixes `tag_score.py`'s tool-sequence-only
clusters, where every git intent collapses into one `Bash>Bash` bucket). Write a
ticket naming the exact room/flow file and the verdict.

### ④ Author — the expectation is author-owned but **evidence-backed**

A flow fixture *is* its outcomes (it stubs each host call's result and asserts
`expect_world`). The author writes the stub + assertion, but the recorded `is_error`
+ stdout tells them **what the stub should return and what world the run produced**,
so the assertion reflects what actually happened rather than memory. When the real
run was messy, the author pins the *intended* outcome and flags the transcript as
improved-upon; when clean, pins it as-observed. Every change ships a green
`kitsoki test flows <story>`, **no live LLM in fixtures** (stub the gates, per
[AGENTS.md](../../AGENTS.md)).

### ⑤ Record — an honest metric

Track **# in-scope intents that CONFORM** out of the **deduped** in-scope total.
Re-mine after a burst of dogfooding; DIVERGES / FIXTURE-GAP / COVERAGE-GAP should
fall as you close them. Where a gate's recovered *results* are dominated by one
branch, that is a determinism-ladder (L2→L3) candidate.

## The profile — scope config, co-located with the story

The one reusable per-story artifact is a small profile,
`stories/<story>/mining.profile.yaml`, so the story self-describes its mine. It is
**scope configuration, not an auto-classifier**: the `scope.grep`/`sample`/`max`
prefilter, the in-scope `action_tags`, the candidate-room `owns` map (a *starting
point* for the map, not an assertion), and the `non_goals` + `non_goal_markers`
(matched against `user_text`, since the 3 git action tags can't separate a commit
from a force-push). See [`stories/git-ops/mining.profile.yaml`](../../stories/git-ops/mining.profile.yaml).

## `coverage_prep.py` — mechanical data-prep (no verdicts)

`coverage_prep.py --job-dir <emit output> --profile <profile>` produces the inputs
the ② map needs and **nothing it shouldn't**:

- `intents.git.json` — scope-filtered intents, each joined to its recipe with the
  Phase-1 `outcome` and `satisfaction` inlined.
- `coverage.md` — a worksheet **skeleton**: a per-intent table with a **blank
  Verdict column** the human/LLM fills, plus the arg-aware frequency table.

It scope-filters, dedups by command-shape, ranks by frequency, joins candidate
rooms, inlines outcomes, and hints non-goals. It **assigns no verdicts and reads no
room bash** — that is the irreducible ② step.

## What remains (deferred)

The doc + profile + Phase-1 outcome capture + `coverage_prep.py` are landed and
proven on git-ops. Two extensions are deliberately deferred:

- **A runnable `story-mining` story** — generalize this loop into a kitsoki story
  whose map gate is the conformance verdict, judge-polymorphic. Highest investment;
  justified only if this becomes a recurring multi-story program. It inherits ②'s
  human/LLM-in-the-loop map — there is no "for free" generalization.
- **Phase-2 sharpening** — determinism scoring that reads `corrected`/`is_error`
  (needs `outcomes.py` to run before `tag_score.py`); target-overlap precision and
  multi-span lookahead on the satisfaction flag.
