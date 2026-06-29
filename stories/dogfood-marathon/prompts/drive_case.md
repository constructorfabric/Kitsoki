You are driving ONE case through an inner kitsoki pipeline for a dogfood
marathon, then reporting the deliverable for an INDEPENDENT verify. You do NOT
self-grade — an oracle decides solved/partial/failed afterward.

Case:

```
{{ context.case }}
```

- Inner pipeline: `{{ context.inner_pipeline }}` (bugfix / delivery-tail / ship-it)
- Maker profile (the model): `{{ context.profile }}`
- Baseline policy: `{{ context.baseline_policy }}`

Drive the inner pipeline to its terminal exit following the dogfood-marathon
method (see `.agents/skills/dogfood-marathon/SKILL.md`). The hard requirements:

- A FRESH per-case worktree (never shared between cases — that IS bug #9).
- Cut the worktree from the **baseline SHA** (per `baseline_policy`: `<fix>^` for a
  merged-fix case, current main for a live ticket) so the bug actually reproduces.
- Pass an **explicit trace path** so the cost/token evidence is recoverable.
- A **scoped test_cmd** restricted to the changed-area packages — a repo with
  pre-existing unrelated reds would bounce every fix forever.

When the pipeline reaches its exit, **report — do not grade**. Submit:

```
{
  "exit": "shipped | needs-human | not-reproducible | abandoned",
  "worktree": "<path>",
  "branch": "<feature branch>",
  "deliverable_present": true|false,   // are the fix files + key edit actually on the worktree?
  "trace": "<trace path>",
  "cost_usd": <number>,                // summed from the trace's payload.meta.cost_usd
  "tokens": <number>,                  // primary, provider-neutral axis
  "wall_s": <number>,
  "findings": [ { "id": "...", "title": "...", "target": "story|kitsoki", "note": "..." } ]
}
```

Do not claim a deliverable that isn't on the worktree. `deliverable_present:false`
with an honest exit is correct when the maker produced nothing — the verify oracle
relies on you reporting the real state, not a hopeful one.
