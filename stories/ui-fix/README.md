# ui-fix — UI audit → patterns → fix loop → before/after gallery

Review a `kitsoki-ui-review` audit into root-cause groups, fix each group with a
scoped agent agent, prove each fix cleared with a deterministic re-audit, and
record a before/after media artifact per group, and emit a deterministic
report/deck bundle for terminal review.

## Entry

Root state: `idle`. No required `world_in:` keys — all configurable world keys
have sensible defaults. Supply `verdict_path` if your audit lives elsewhere.

```yaml
world_in:
  verdict_path:   ".artifacts/ui-review/verdict.json"   # default
  frames_dir:     ".artifacts/ui-review/frames"         # default
  src_root:       "tools/runstatus/src"                 # default
  severity_floor: "warn"                                # error | warn | info
  media_format:   "slideshow"                           # slideshow | video
```

## Exits

| Name | Meaning | World keys populated |
|---|---|---|
| `done` | All groups processed (cursor past end, or no findings at floor). | `fixed`, `skipped`, `still_failing`, `report_path`, `summary_path`, `deck_path` |
| `abandoned` | Operator typed `quit`. | `abandon_reason` |

## Rooms

```
idle → review → fixing ←──────────────────────────────┐
                  │ accept                             │
                  ▼                                   │
              verifying ──(regressed)─────────────────┘
                  │ cleared
                  ▼
              showcase → (next) → fixing / done
                                         │
                                         ▼
                                        done
```

| Room | What happens |
|---|---|
| `idle` | `host.run` reads verdict.json. `host.starlark.run dedupe_findings.star` collapses geometry/a11y duplicates by fingerprint and drops findings below `severity_floor`. Routes to `review` (non-empty) or `done` (empty). |
| `review` | `host.agent.decide` clusters deduped findings into ranked root-cause groups. Operator confirms, reorders, merges, or drops before `start`. |
| `fixing` | `host.agent.task` applies the minimal fix for one group (scoped to `src_root`). Checkpoint: `accept` / `refine feedback=…` / `skip` / `quit`. |
| `verifying` | `host.run` re-runs capture.sh (geometry+axe only, no LLM). `host.starlark.run verify_delta.star` checks if all member fingerprints cleared. Routes to `showcase` (cleared) or back to `fixing` (regressed). |
| `showcase` | `host.contact_sheet` (slideshow) or `host.slidey.render` (video) produces a before/after media artifact. `host.artifacts_dir` emits it and returns a stable handle. Rendered inline via `media` view element. |
| `done` | Writes `.artifacts/ui-fix/<run>/report.md`, `summary.json`, and `deck.slidey.json`, then shows the gallery of fixed groups (before/after media), skipped groups, and still-failing groups. Points at `kitsoki-ui-review` for a full re-review. |

## Host requirements

| Handler | Used in |
|---|---|
| `host.run` | idle (cat verdict.json), verifying (capture.sh, cat audit.json), done (deterministic report/deck script) |
| `host.starlark.run` | idle (dedupe_findings.star), verifying (verify_delta.star), fixing/showcase/verifying (get_current, accumulate, manage_groups) |
| `host.agent.decide` | review (identify_patterns.md) |
| `host.agent.task` | fixing (apply_fix.md) |
| `host.contact_sheet` | showcase (slideshow mode) |
| `host.slidey.render` | showcase (video mode; degrades on on_error) |
| `host.artifacts_dir` | showcase (media-emit path) |

## Non-goals

- **Vision re-verify** — per-group verify is geometry+axe only. Run `kitsoki-ui-review`
  manually for a full vision pass after the session.
- **Cross-group fixes** — each `host.agent.task` targets exactly one group.
- **Anything outside `src_root`** — the fix agent is scoped to `tools/runstatus/src/`.

## Flow fixtures

| Fixture | Scenario |
|---|---|
| `happy_path.yaml` | Full pipeline: idle → review → 2 groups fixed → done. |
| `empty_findings.yaml` | No findings at floor → short-circuit to done. |
| `review_merge_then_start.yaml` | Merge two groups, then start. |
| `refine_once_then_accept.yaml` | Refine once (group_cycle→1), then accept. |
| `refine_budget_exhaust_skip.yaml` | Hit group_budget → auto-skip, advance cursor. |
| `verify_regression_then_fix.yaml` | Verifying finds members still present → back to fixing. |
| `showcase_slideshow.yaml` | contact_sheet + artifacts_dir stub → media_handle bound. |
| `quit_mid_loop.yaml` | Quit mid-loop → @exit:abandoned, partial fixed preserved. |
