---
name: dogfood-marathon
description: Process a backlog of real cases (bugs / deliveries / ship-its / tickets) by driving a kitsoki story (bugfix, delivery-tail, ship-it, …) LIVE over each one via the kitsoki-mcp-driver agent, independently verifying every deliverable, capturing friction as findings, and hardening the story/prompts for GENERAL use WITHOUT OVERFITTING to the cases in the run. Use when the user says "run a dogfood marathon", "smash these bugs through bugfix", "process this backlog live", "dogfood <story> over these tickets and fix the pipeline", or wants a marathon report (outcomes / verdicts / cost / fixes shipped / findings). Distinct from story-coverage-mining (transcript-driven coverage) and matrix-task-comparison (harness×model bake-off on fixed tasks).
---

# Dogfood marathon

Take a backlog of real cases and drive a kitsoki story **live** over each one,
treating every point of friction as a chance to **harden the pipeline for the
general class** — never to paper over this one case. The output is fixed
deliverables (or honest needs-human parks), a set of findings, generic
prompt/story hardenings, and a marathon summary.

The discipline that makes it worth anything: **improve without overfitting**,
and **trust only an independent verify** — never the maker's self-report.

**Read first (don't re-derive):**
- [`.context/bakeoff-learnings.md`](../../../.context/bakeoff-learnings.md) — the full method, the 13 hard-won gotchas, the dogfood findings (F1–F5), the no-overfit principle. This skill is the *runbook*; that doc is the *evidence*.
- [`stories/bugfix/README.md`](../../../stories/bugfix/README.md) — the pipeline being driven: rooms, exits (`shipped` / `needs-human` / `not-reproducible`), the RED→GREEN regression gate, `bugfix_mode=triage`.
- The hardened prompts under [`stories/bugfix/prompts/`](../../../stories/bugfix/prompts/) — what a legitimate generic fix looks like (commit `d210ea67`).
- [`issues/bugs/README.md`](../../../issues/bugs/README.md) + the ticket frontmatter for how findings become filed tickets.
- MEMORY: `workflow-gate-on-independent-verify`, `workflow-dead-impl-poisons-demo`, `bugfix-pipeline-verify-gaps`, `bugfix-triage-mode`, `set-block-atomic-determinism`.

## The marathon loop (per case)

Run the whole loop through the **`kitsoki-mcp-driver`** agent — the studio MCP is
the only write surface; you never touch the filesystem to drive the story.

> **Headless?** The in-process Agent/subagent path does NOT attach the studio MCP
> (it inherits the parent's empty MCP set → "No MCP servers configured"). To drive
> from a script/cron/another agent, use the raw-`claude -p` primitive
> [`tools/mcp-drive/drive.sh`](../../../tools/mcp-drive/README.md) (`--mcp-config`
> + `--strict-mcp-config`); the live worker model is chosen per session by
> `session.new {profile, harness:"live"}`. See MEMORY `mcp-first-delegation-runbook`.

1. **Pick the case** and pin its **baseline**. The baseline is the buggy state
   the pipeline must reproduce against — for a merged-fix case that is the fix's
   PARENT commit (`<fix>^`); for a live ticket it is current main (or the SHA the
   ticket was filed against). Confirm the bug is actually present at the baseline.

2. **(Optional) triage-only pre-flight.** Cheap read-only verdict before spending
   maker budget. Drive bugfix with `bugfix_mode=triage` → the triaging room emits a
   standardized verdict: `ALREADY-FIXED | STILL-LIVE | PARTIAL | UNCLEAR` (read-only,
   no worktree). Drop `ALREADY-FIXED` cases (a test/lint layered on an already-merged
   behavioural fix → baseline is GREEN → degenerate); only run the full pipeline on
   `STILL-LIVE` / `PARTIAL`. See MEMORY `bugfix-triage-mode`.

3. **Drive the full pipeline live** via `session_new` (through kitsoki-mcp-driver):
   - `harness: live`, an explicit `profile:` (the profile, not the story agent-def,
     picks the maker model — see Gotchas),
   - an **explicit `trace:` path** — otherwise the session writes its trace to a
     random temp file and the cost/token evidence is unrecoverable (the filed P1
     missing-trace bug; gotcha #12),
   - `base` / `base_branch` = the **baseline SHA** so the pipeline cuts its worktree
     from the buggy parent, not main where the bug is already gone (gotcha #11),
   - a **scoped `test_cmd`** restricted to the changed-area packages — a repo with
     pre-existing unrelated reds would bounce every fix forever (gotcha #13),
   - a **hermetic per-case worktree** — cells/cases must NEVER share a worktree;
     sharing IS bug #9 (gotcha #1).
   Then drive the rooms (`start` → reproducing → … → testing → tail) and answer
   judge checkpoints as an honest operator would.

4. **Independently VERIFY the deliverable — never trust the maker's self-report.**
   Run the oracle / targeted tests **yourself** on the produced worktree or merged
   commit. The maker "submitted" or returned success means nothing on its own — an
   impl agent that dies mid-response can still have landed (or half-landed) edits,
   and a weak maker confabulates "submit missing." Gate on **deliverable existence**
   (the files + the key edit are present) AND a clean oracle pass — not on build-green
   alone and not on the agent's return. (MEMORY: `workflow-gate-on-independent-verify`,
   `workflow-dead-impl-poisons-demo`, `bugfix-pipeline-verify-gaps`.)

5. **Record the outcome.** verdict, exit (`shipped` / `needs-human` / `not-reproducible`),
   independent-verify result (PASS/FAIL and how you checked), cost (sum the trace's
   per-call `payload.meta.cost_usd`) + tokens (primary, provider-neutral axis), wall
   time, and every friction point as a **finding** (next section).

The **regression gate parks un-instrumented tickets at `needs-human`** (a ticket
with no seeded `repro_command` → `regression_red_pre_fix=false` → never auto-ships).
That is **correct** RED→GREEN discipline, not a failure: the maker still produces
the fix; a human verifies + merges. Expect it for a marathon over tickets that lack
repro commands; don't "fix" it.

## Driving mechanics & gotchas (condensed)

| # | Gotcha | Why |
|---|---|---|
| explicit `trace:` | pass a real trace path to `session_new` | else trace lands in `$TMPDIR` random file → cost evidence lost (P1) |
| `base`=baseline SHA | cut the worktree from `<fix>^` / the buggy SHA | main has the bug fixed → nothing reproduces |
| scoped `test_cmd` | restrict CI to changed-area packages | pre-existing unrelated reds bounce every fix forever; your oracle is authoritative |
| per-case worktree | one fresh worktree per case, never shared | shared worktree IS bug #9 |
| maker model = profile | set the model via session `profile:`, not story `model:` | profile supersedes the agent-def (`claude-native`→opus, `claude-sonnet`→sonnet, `synthetic-claude`→GLM, `codex-native`→gpt) |
| needs-human park | un-instrumented ticket parks at needs-human | expected RED→GREEN discipline; human verifies+merges |
| CLEAN pre-flight | before each case `git worktree remove --force` AND `git branch -D fix/<id>` | removing only the worktree dir leaves the branch; `workspace.create` silently REATTACHES it → the run inherits a prior run's commits (bug already "fixed" → not-reproducible). Delete BOTH. |
| heartbeat watchdog | watch the trace mtime; kill on stall, NOT on wall-clock | a live run can legitimately take hours; a STUCK one stops growing the trace. See below. |

### Background runs: clean pre-flight + a heartbeat watchdog

A long live dogfood is driven by a background agent and can run for **hours** —
that's fine *as long as it's making external progress*. The failure to guard
against is the agent getting **truly stuck** (a blocked studio call, a hung
maker, an owner-marker collision) and waiting forever with no signal.

- **Pre-flight CLEAN** (per case): `git worktree remove --force .worktrees/bf-<id>`
  AND `git branch -D fix/<id>` AND `git worktree prune`. Removing only the
  worktree leaves the branch for `workspace.create` to reattach — a poisoned run.
- **Monitor via the FILESYSTEM, not the MCP.** You can't check a running job
  with a second `session.status` — the MCP client serialises tool calls per
  connection and sessions are per-process (see `studio.handles` ticket; it's a
  client/topology limit, NOT a server bug — the server is provably concurrent).
  Use the trace instead: **`kitsoki trace status <trace.jsonl>`** (or `--json`)
  prints `{state, turn, status, last_error, cost, idle-time}` and flags
  **⚠ STALLED** when a non-terminal run has gone idle — the one-shot, cross-process
  way to check on a live drive. Also peek the worktree `git log`.
- **Heartbeat watchdog** — launch it in the background ALONGSIDE the dogfood,
  pointed at the run's trace; when it exits STALLED, `TaskStop` the stuck agent
  and report (don't wait):
  ```sh
  # dogfood writes its trace to .artifacts/<run>/trace.jsonl (--trace-out / driver)
  .agents/skills/dogfood-marathon/scripts/heartbeat-watch.sh \
    .artifacts/<run>/trace.jsonl 600 60   # STALL=10min no-growth, POLL=60s
  ```
  A healthy run refreshes the trace every turn, so the watchdog never trips on a
  genuinely-busy multi-hour run — only on a stuck one.

## Improve WITHOUT overfitting (the core discipline)

When the pipeline fails a case, fix the **story / prompts generically** — a change
is legitimate only if it helps the **general class**, not just this case. The
worked example: the blind-implementer failure on one bug drove a generic hardening
(self-verifying implementer + RED-now reproducer + honest test_author + smallest-fix
proposer) — commit `d210ea67` — phrased so it helps any bug, naming none.

`stories/AGENTS.md` is load-bearing here: **never paper-over, hack, or work around
runtime issues** — the stories exist to expose them through real use and force a
real fix. A `set:`-block patch is fine (atomic vs a frozen pre-block snapshot —
MEMORY `set-block-atomic-determinism`); a case-specific special-case is not.

**"Is this change overfitting?" checklist — a fix must pass ALL of these:**
- [ ] Does it name a specific case / ticket / bug number? → if yes, it's overfit.
- [ ] Does it only help these particular inputs? → if yes, it's overfit.
- [ ] Would it help an **unseen** bug of the same class? → if no, it's overfit.
- [ ] Is it a generic prompt/room/gate change, not a hardcoded value or branch for this case?
- [ ] Is the runtime issue **surfaced + fixed**, not swallowed by an `on_error:` arc or a workaround?
- [ ] Did you add a **no-LLM flow regression** proving the general behaviour (cassette-stubbed, never a real oracle)?

If a change fails any box, it's overfit — generalise it or drop it.

## Capturing findings → tickets

Each friction point becomes a concrete **finding**; the consequential ones become
filed tickets via the studio `issue_create` tool (through kitsoki-mcp-driver), in
the `issues/bugs/` frontmatter shape (`id`, `title`, `target: story|kitsoki`,
`severity`, `component`, `trace_ref`, Body / Expected / Actual / Impact). Worked
examples from the 2026-06 bugfix marathon:

- **F1 (fixed):** blind implementer (told not to run tests) shipped a 65-test-breaking
  fix → hardened generically in `d210ea67`.
- **F2:** `needs-human` parking surfaces the regression-gate technicality instead of
  the louder "your fix breaks N tests" — under-reports severity.
- **F3:** a `needs-human` park leaves the worktree/branch uncleaned with no surfaced
  path / resume / cleanup hint.
- **P1 missing-trace:** `session_new` writes the trace to `os.CreateTemp` instead of
  `store.DefaultTracePath` → filed
  `issues/bugs/2026-06-24T090000Z-mcp-live-sessions-no-discoverable-trace.md`.

A finding that points at a runtime/story bug → file the ticket AND, where the class
warrants it, add the no-LLM flow regression that pins the fix.

## Honesty controls (non-negotiable)

- **Independent verify, oracle-gated.** Run the oracle/tests yourself; the deliverable
  is graded by your oracle, not the pipeline's internal CI status and not the agent's
  return string.
- **Don't trust agent returns.** A `failed:` or dead agent can still have landed edits;
  re-verify the actual commit/worktree after any such agent. Abort before any
  demo/report step if a deliverable is null (no lookalike substitution).
- **Cost on one consistent basis.** Sum the trace's native `payload.meta.cost_usd`;
  report TOKENS as the primary provider-neutral axis, USD second.
- **No real LLM in automated tests.** The live marathon drive is a manual, gated action.
  Every regression you add is cassette-stubbed (`stories/AGENTS.md`, root `AGENTS.md`).

## Output: the marathon summary

Produce a summary table + narrative covering, per case: **case · triage verdict ·
outcome/exit · independent-verify result · cost (tokens primary, USD) · wall time**,
then roll up: **fixes shipped · findings (with ticket links) · prompt/story hardenings
(generic only)** and the honest headline (structure isn't automatically cheaper, but
it's more thorough — regression test, safe gate-parking, refine loop — and catches
bad fixes a naive single prompt would ship).

This summary is the same payload the **`dogfood-marathon` kitsoki story** (built in
parallel under `stories/dogfood-marathon/`) wraps into a drivable workflow that emits
a **slidey report** of outcomes / effectiveness / time / cost / what-fixed /
what-worked / what-didn't. When that story exists, prefer driving it (it journals the
per-case data for the deck); use this skill's by-hand loop to bootstrap it or for
one-off marathons.

## Runbook (crisp)

1. Assemble the backlog + pin each case's baseline; confirm the bug is present at baseline.
2. (Optional) triage-only pass (`bugfix_mode=triage`); drop `ALREADY-FIXED`.
3. Per case, via **kitsoki-mcp-driver**: `session_new` (`harness:live`, `profile:`,
   explicit `trace:`, `base`=baseline, scoped `test_cmd`, fresh worktree) → drive rooms.
4. **Independently verify** the deliverable yourself (oracle + deliverable-existence).
5. Record outcome + cost/tokens + time + findings.
6. For each failure: harden the **story/prompts generically** — run the overfitting
   checklist; add a no-LLM flow regression; file tickets for runtime/story bugs.
7. Emit the marathon summary (and feed it to the `dogfood-marathon` story's slidey deck).

## Maintenance

Codex discovers this skill directly. After adding/moving it, re-link into Claude
Code's `.claude/skills/`:

```
make setup
```
