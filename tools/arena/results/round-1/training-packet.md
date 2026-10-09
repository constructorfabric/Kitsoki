# WB.4 round 1 — training packet

**process_version:** v1 (pre-training baseline; `dispatch_kitsoki` fix from
commits 6690d0d9/58a9182a/d315e581/63c867ef is the process under test, not a
patch produced BY this round — see §7 caveat below).

## Scope actually covered

The frozen training split (`docs/goals/generalized-usage` WB wall,
`tools/arena/corpus/cost-bench.manifest.yaml`) has 4 `bugfix_test_repair`
training tasks. Round 1 now covers **all 4**, across two sessions:

- `query-string-qs1-bugfix-test-repair` — real live data reused from the WB.3
  pilot validation (not re-run; see `tools/arena/results/pilot/live-run/`).
- `query-string-qs2-bugfix-test-repair` — live cells from the first round-1
  session.
- `kitsoki-bug9-bugfix-test-repair` and `kitsoki-bug12-bugfix-test-repair` —
  live cells from a second session, after fixing the structural gap that
  blocked them (below).

Only the `codex-native` candidate ran (both treatments use the identical
worker model, per the WB.4 fairness requirement); `synthetic-claude` is a
zero-cost negative control already proven in the WB.3 pilot and wasn't
re-run.

### Infra fixes that unblocked bug9/bug12 (both real, both committed)

1. **Docker git-worktree mount.** `kitsoki-bug9`/`kitsoki-bug12` reference
   `project: kitsoki` (this repo's own history). A git-worktree checkout's
   `.git` is a file pointing at an absolute host path into the primary
   checkout's `.git/worktrees/<name>`, which doesn't resolve inside a
   container that only mounts the worktree directory. Fixed by bind-mounting
   the primary checkout's `.git` at the identical path, read-only
   (`tools/arena/arena.py`'s `_primary_git_dir_for_worktree`).
2. **`git clone --local` cross-device hardlinks.** Once the mount above
   worked, arming still failed with `Invalid cross-device link` — `--local`
   defaults to hardlinking objects, which fails once the source is a
   read-only bind mount on a different filesystem than the `/tmp` clone
   destination. Fixed with `--no-hardlinks` on both `--local` clone sites
   (`paired_task_runner.py`'s `materialize_baseline`, `bench.py verify`'s
   private mirror).

Both fixes were verified live (no-LLM/free arm-only checks) before any
paid cell ran.

## Results (4 tasks x 2 treatments, n=1 each — 8 cells, all real, no fabrication)

| task | treatment | verdict | cost_usd (cache-aware) | tokens |
|---|---|---|---|---|
| qs1 | kitsoki | solved | $0.44 | 931,778 |
| qs1 | single-briefed | solved | $0.48 | 85,622 |
| qs2 | kitsoki | solved | $0.52 | 1,145,873 |
| qs2 | single-briefed | solved | $0.32 | 56,990 |
| bug9 | kitsoki | **failed** | $3.44 | 15,888,760 |
| bug9 | single-briefed | **failed** | $0.85 | 150,399 |
| bug12 | kitsoki | **failed** | $1.24 | 3,493,106 |
| bug12 | single-briefed | **failed** | $1.38 | 244,732 |

Aggregate across all 8: win-rate 0.5, kitsoki avg $1.41, single-briefed avg
$0.76. Total real spend across both round-1 sessions: **~$8.15** (qs2 +
bug9 + bug12 cells; qs1 reused from the WB.3 pilot).

## ⚠️ bug9/bug12's `failed` verdicts are likely CONFOUNDED, not clean signal

Both `kitsoki`-treatment threads (`.thread.md` postmortems) self-reported
completion and claimed "Validation completed," yet the arena's independent
oracle scored both `oracle=fail`. Both threads explicitly say the same
thing: *"the local environment did not have `go` on PATH, so I could not
re-run that command"* / *"this container did not have `go` on PATH."*

Root-caused and **fixed** after these cells ran: the paired-task Docker
image's `golang:1.25-bookworm` base sets Go/Cargo paths via `ENV PATH`, which
only reaches non-login shells. The live worker's `host.Bash` tool runs
commands via `bash -lc` (a login shell), which re-sources `/etc/profile` —
Debian's default there unconditionally overwrites the inherited PATH with a
bare system path, dropping `/usr/local/go/bin` entirely. Verified: `bash -lc
'which go'` failed before the fix, succeeded after. Fixed in
`tools/arena/docker/Dockerfile.paired-task` via a `/etc/profile.d/` script
(login shells source those; `ENV` does not reach them).

**Correction after deeper tracing (do not overstate this confound):** the
`failed` verdict itself is NOT explained by the PATH bug. Scoring runs
through `bench.py`'s `sh()` helper (`subprocess.run(cmd, shell=True,
env={**os.environ, ...})`) — a plain non-login subprocess that inherits the
calling Python process's own environment directly, with no `/etc/profile`
re-sourcing. `go` was on `PATH` there the whole time; verified by reading
the actual scoring code path (`tools/bugfix-bakeoff/external/bench.py`
`score()` → `sh(run_cmd, ...)`). Kitsoki's hidden hidden regression-test
oracle for bug9/bug12 (a real, pinned test from this repo's own historical
fix, injected only at scoring time) genuinely did not pass against either
treatment's fix. Corroborating evidence: the **single-briefed** cells'
own self-authored verification (via absolute `/usr/local/go/bin/go` paths,
visible in their real trace output) ran and PASSED, yet those cells *still*
failed the hidden oracle — so the failure is a real result about fix
correctness against the specific pinned oracle, not a scoring-side infra
artifact.

The PATH bug's actual, narrower effect: only the **kitsoki-treatment**
agent's own self-verification (via the `host.Bash` tool, which runs through
`bash -lc`) was broken, so those two threads falsely reported "validation
completed" without having actually run their own tests. That's a real,
separate finding — a live worker should not report a fix as verified when
its own verification tool call errored out — worth carrying into the
story/prompt layer once training patches start (not yet actioned this
round; see anti-overfit note below). It does NOT mean bug9/bug12's `failed`
verdicts are untrustworthy; a re-run under the fixed image might still
change the *kitsoki*-treatment outcome (a worker that can actually see its
own test failures might iterate further before giving up), but there's no
reason to expect it changes the *single-briefed* result, and no basis to
expect the hidden oracle would suddenly pass either way. A clean re-run is
still worth doing to get an honest read on the kitsoki treatment
specifically, but it should not be read as "the current failed verdicts are
probably wrong."

## Analysis gate (against docs/research/cost-efficiency-benchmark.md §2 criteria)

**Still not evaluated against H1-H4.** The training split now has real data
for 4/20 tasks (1 of 4 archetypes). Declaring a verdict off a 4-task, 1-
archetype subset would violate the anti-overfit/fair-sample discipline the
round protocol itself requires, independent of the PATH-bug question above.

## Training pass

**None landed.** Per §7's lifecycle, a training pass (failure mining →
generic patches → ratchet → version bump) is warranted when the analysis
gate says "not met" on real evidence across a fair sample — a single
archetype at 4/20 tasks isn't that yet, regardless of the PATH-bug question.
The self-verification-silently-skipped finding IS a legitimate candidate for
the eventual training pass (a generic guard: a live worker should not report
a fix verified when its verification tool call itself errored/was
unavailable) — flagged here for later mining, not actioned this round.

## Recommended next round

1. Re-run `kitsoki-bug9`/`kitsoki-bug12`'s `kitsoki`-treatment cells under
   the fixed Docker image (Go now reachable from `bash -lc`) for a cleaner
   read on the kitsoki-treatment process specifically — not because the
   current `failed` verdicts are expected to be wrong (see correction
   above: the oracle itself was never affected by the PATH bug), but
   because a worker that can actually see its own test results might
   iterate further before giving up. Small, bounded (~$1.20-3.50/cell based
   on this round's costs).
2. Extend to the remaining 3 archetypes' training tasks (`docs_site_release`,
   `git_ops_landing`, `ui_visual_qa` — 12 more training tasks) before any
   analysis-gate verdict is drawn; even a clean 4-task sample is too narrow
   to support H1-H4 either way.
3. `vscode-01-docs-site-release` (held-out, not training) was refreshed
   under the dispatch fix this round for infra-validation purposes — see
   `tools/arena/results/pilot/live-run/CAVEATS.md` for why it does NOT count
   toward the eventual WB.5 confirmation run's "held-out executed once"
   discipline.
