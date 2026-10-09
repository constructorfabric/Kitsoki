# Reduced System Prompt Five-Bug Study

**Status:** live run attempted twice; not a completed causal A/B.
**Prepared:** 2026-07-06.
**Live LLM calls:** yes. One clean GLM-5.2 cell reached a terminal model result
and was hidden-oracle scored. A second attempt at the remaining nine cells
produced traces for all five `old-append-default` cells, but each stalled and
was scored `oracle_pass: false` / `cost_usd: 0.0` — a runner permission bug,
not a model failure (see "Second Attempt" below). The bug is now fixed; a
third pass is needed to produce trace-backed results for the remaining cells.

## Question

Does replacing the harness default prompt with Kitsoki's focused project prompt
reduce cost without degrading bug-fix performance?

The change under test is `f97e340a sysprompt: load project base prompt`:

- `old-append-default`: `822f358445b9ef4c70898ced83c554ce033a8b6b`
  (`f97e340a^`).
- `focused-replace`: `e2c2c1506f8a5b8710feee514d8593c5b924390d`
  (local `main` at run time, containing `f97e340a`).

This is a prompt-transport and prompt-focus test, not a model benchmark. The
required causal comparison remains the same bug corpus, same model/profile, same
guidance policy, same scoring oracle, and only the system-prompt path changed.

## Live Run Summary

Candidate profile: `glm-5.2` / `synthetic-claude` / GLM-5.2 medium. This was the
first fixed metered profile because its traces carry native USD.

No-cost preflight passed before spending:

- `query-string`: `qs1`, `qs2`, `qs3` were RED at baseline and GREEN at the real
  fix.
- `kitsoki`: `bug9`, `bug14` were RED at baseline and GREEN at the real fix.

The clean live run used `/tmp/kitsoki-rsp-study/...` for live cell worktrees and
traces so the worker prompts did not point at prior Kitsoki `.artifacts`
bake-off outputs. Review artifacts were copied to
`.artifacts/reduced-system-prompt-five-bug-study/clean-2026-07-06/...`. Earlier
in-repo attempts are invalid pilot evidence because a worker read prior
`.artifacts/external-bakeoff` files during review.

| Variant | Bug | Project | Result | Oracle | Cost | Agent calls | Trace |
|---|---|---|---|---|---:|---:|---|
| `old-append-default` | `qs1` | query-string | solved | pass | $1.3704 | 10 | `.artifacts/reduced-system-prompt-five-bug-study/clean-2026-07-06/old-append/traces/query-string-qs1-glm-5.2.jsonl` |

The scored cell JSON is:

```text
.artifacts/reduced-system-prompt-five-bug-study/clean-2026-07-06/old-append/results/cells/query-string-qs1-glm-5.2-kitsoki.json
```

The trace classifier reports `model:result`: the pipeline reached a terminal
state, all 10 agent calls completed, and the hidden oracle passed. A contamination
scan found zero reads of `/Users/brad/code/Kitsoki/.artifacts` or
`external-bakeoff` in that trace.

## Matrix Attempt

The intended 10-cell matrix was:

| Axis | Values |
|---|---|
| Prompt variant | `old-append-default`, `focused-replace` |
| Bugs | `qs1`, `qs2`, `qs3`, `bug9`, `bug14` |
| Candidate | `glm-5.2` fixed metered profile |
| Treatment | Kitsoki pipeline only |

Only `old-append-default / qs1` produced a valid model result. The remaining
nine cells failed before `session_new` wrote a trace:

| Variant | Pending cells | Health class |
|---|---:|---|
| `old-append-default` | 4 | `infra:no-trace` |
| `focused-replace` | 5 | `infra:no-trace` |

Representative pending results:

```text
.artifacts/reduced-system-prompt-five-bug-study/clean-2026-07-06/focused-replace/results/cells/query-string-qs1-glm-5.2-kitsoki.json
.artifacts/reduced-system-prompt-five-bug-study/clean-2026-07-06/old-append/results/cells/query-string-qs2-glm-5.2-kitsoki.json
.artifacts/reduced-system-prompt-five-bug-study/clean-2026-07-06/focused-replace/results/cells/kitsoki-bug14-glm-5.2-kitsoki.json
```

The drive stderr/stdout logs for those no-trace failures were empty, so the
runner did not preserve the underlying orchestrator/MCP startup error. These are
not model misses and cannot be counted in solve-rate or cost-per-solve.

## Runner Findings

Two live-run issues had to be made explicit before any result could be trusted:

1. `agent.ask` submit permission deadlock. The first old-append `qs1` attempt
   reached the proposer, then Claude requested permission for
   `mcp__validator__submit` and could not proceed headlessly. The study branch
   adds `BAKEOFF_CLAUDE_ALLOW_VALIDATOR=1`, which writes a per-cell
   `.claude/settings.local.json` allowing only `mcp__validator__submit` and
   `mcp__operator__ask` in the disposable worktree. The same runner-only setting
   was used for both variants.
2. Worktree location contamination. An in-repo attempt let a worker discover
   prior `.artifacts/external-bakeoff` files during test review. The clean run
   moved live cells to `/tmp/kitsoki-rsp-study/...`, copied review artifacts to
   `.artifacts/reduced-system-prompt-five-bug-study/clean-2026-07-06/...`, and
   scanned the trace for those artifact paths.

These are harness quality findings, not evidence for or against the reduced
system prompt.

## Second Attempt: `session_status` Permission Deadlock

A follow-up live pass fixed the no-trace failure above (drivers now surface the
underlying error) and produced full ~1.1 MB / 180+-line traces for all five
`old-append-default` cells (`qs1`, `qs2`, `qs3`, `bug9`, `bug14`). Every cell
still scored `oracle_pass: false`, `suite_pass: false`, `cost_usd: 0.0`,
`agent_calls: 0` despite the substantial trace — a second infrastructure bug,
not a model miss.

The `qs1` transcript shows the proposer explicitly blocked:

> I'm blocked — the `session_status` tool call keeps getting denied/unapproved.
> Could you approve permission for `mcp__kitsoki__session_status` (and likely
> the other kitsoki MCP tools I'll need: `session_world`, `session_trace`,
> `session_close`)...

Root cause: `drive_cell.sh` writes a per-cell `.claude/settings.local.json`
(guarded by `BAKEOFF_CLAUDE_ALLOW_VALIDATOR=1`) that only allowlisted
`mcp__validator__submit` and `mcp__operator__ask`. The proposer needs to poll
`session_status`/`session_world`/`session_inspect`/`session_trace` to drive the
pipeline to completion headlessly, and those calls were silently denied (no
operator surface is attached in this harness, so there is nothing to approve
them). The agent burned real tokens (`$0.44` on this one call alone) stuck
retrying a denied tool instead of producing a scoring-eligible result.

**Fixed** at `b94da0e2`: `drive_cell.sh`'s generated settings and
`mcp-drive/drive.sh`'s default `TOOLS` now both allowlist
`session_status`/`session_world`/`session_inspect`/`session_trace`.

## Conclusion

The study still does **not** prove that `focused-replace` is cheaper or equally
effective. It now contains one real, cost-bearing, hidden-oracle-scored model
result:

> GLM-5.2 under the old prompt path solved `query-string/qs1` for $1.3704 across
> 10 agent calls.

That is not an A/B. A research-grade prompt result requires the remaining cells
to produce trace-backed model results under the same runner isolation, now that
both the no-trace failure and the `session_status` permission deadlock are
fixed. The next live pass is the one that should finally produce a scoreable
10-cell matrix.

Until those cells exist, the only defensible claim is:

> The corpus is armed, one clean live cell is solved, two distinct runner bugs
> (silent no-trace failures, then a silently-denied polling-tool deadlock) have
> been found and fixed from live evidence, and the five-bug prompt A/B still
> needs one more live-drive pass to publish.
