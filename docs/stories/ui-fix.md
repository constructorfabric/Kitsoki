# ui-fix story

**Entry:** `idle` · **Exits:** `done`, `abandoned`

The ui-fix story reviews a `kitsoki-ui-review` audit into root-cause patterns,
applies a minimal scoped fix per group, proves each fix cleared with a
deterministic re-audit, and records a before/after media artifact.

It is the first consumer of the recorded media substrate
(`host.contact_sheet`, `host.slidey.render`, `host.artifacts_dir`).

## Design: interpretive/deterministic split

Every step is either interpretive (LLM judgment, isolated and recorded) or
deterministic (pure computation, replayable):

| Step | Kind | Host |
|---|---|---|
| Load + mechanical dedup | deterministic | `host.run` + `host.starlark.run` |
| Pattern identification | interpretive (agent) | `host.agent.decide` |
| Apply group fix | interpretive (agent) | `host.agent.task` |
| Verify cleared | deterministic | `host.run` + `host.starlark.run` |
| Before/after media | deterministic | `host.contact_sheet` / `host.slidey.render` + `host.artifacts_dir` |

The one interpretive scoping decision lives in one room (`review`) and is
recorded as a gate decision. The fixer never grades its own homework
(`verify_result.cleared` is a measurement, not `fix_result.applied`).

## Room graph

See [`stories/ui-fix/README.md`](../../stories/ui-fix/README.md) for the full
room breakdown, host requirements, and flow fixture catalogue.

## Starlark scripts

Scripts under `stories/ui-fix/scripts/` are all deterministic (no HTTP):

| Script | Purpose |
|---|---|
| `dedupe_findings.star` | Collapse verdict.json findings by `(source, check, selector)` fingerprint; apply severity floor. Reads `ctx.world.verdict_raw`. |
| `verify_delta.star` | Compare fresh audit against group member fingerprints. Reads `ctx.world.verify_raw` and `ctx.world.current`. |
| `get_current.star` | Extract `groups.items[cursor]` as the current group. |
| `manage_groups.star` | Reorder / drop / merge group list (review room). |
| `accumulate.star` | Append current group to fixed/skipped/still_failing bucket. |

## Found problems? Drive this story

If you have a `verdict.json` from `kitsoki-ui-review`, start here:

```
kitsoki run stories/ui-fix/app.yaml
```

Set `verdict_path` and `src_root` in the world if your layout differs from the
defaults. The `review` room proposes groups — confirm scope before the fix loop
starts.
