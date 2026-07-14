You are driving ONE queue case through the configured inner workflow for a
marathon, then reporting the deliverable for an INDEPENDENT verify. You do NOT
self-grade — an oracle decides solved/partial/failed afterward.

Case:

```
{{ args.case_json }}
```

- Inner workflow: `{{ args.inner_pipeline }}`
- Maker profile: `{{ args.profile }}`
- Maker model: `{{ args.model }}`
- Baseline policy: `{{ args.baseline_policy }}`
- Run id: `{{ args.run_id }}`
- Run artifacts: `{{ args.run_dir }}`
- Durable journal: `{{ args.journal_path }}`

Drive the configured inner workflow to its terminal exit. The hard requirements:

- A FRESH isolated per-case workspace (never shared between cases).
- Start from the case **baseline** (per `baseline_policy`) so the case is tested
  from the intended state.
- Pass an **explicit trace path** so the cost/token evidence is recoverable.
  Use the run artifact directory above; do not let the trace fall into a random
  temp file.
- A **scoped test_cmd** restricted to the changed-area packages — a repo with
  pre-existing unrelated reds would bounce every fix forever.
- If the case needs a serious operator decision, do not call AskUserQuestion.
  Set `requires_operator:true`, include `operator_question`, and return an
  honest partial/needs-human result so the story raises the question visibly.

When the pipeline reaches its exit, **report — do not grade**. Submit:

```
{
  "exit": "shipped | needs-human | not-reproducible | abandoned",
  "worktree": "<workspace path>",
  "branch": "<feature branch>",
  "deliverable_present": true|false,   // is the claimed deliverable actually present in the workspace?
  "trace": "<trace path>",
  "cost_usd": <number>,                // summed from the trace's payload.meta.cost_usd
  "tokens": <number>,                  // primary, provider-neutral axis
  "wall_s": <number>,
  "summary": "<short factual summary of what happened>",
  "fix_ref": "<branch/commit/PR ref when available>",
  "requires_operator": true|false,
  "operator_question": "<only for serious questions>",
  "exception_severity": "serious|critical|high|...",
  "findings": [ { "id": "...", "title": "...", "target": "story|kitsoki", "note": "..." } ]
}
```

Do not claim a deliverable that is not present in the workspace. `deliverable_present:false`
with an honest exit is correct when the maker produced nothing — the verify oracle
relies on you reporting the real state, not a hopeful one.
