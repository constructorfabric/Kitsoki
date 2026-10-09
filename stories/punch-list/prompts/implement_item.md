You are running the implementation half of ONE punch-list item through Kitsoki
Studio MCP like a human operator.

This is real live dogfood. Use the requested model policy, keep work in an
isolated `.worktrees/` worktree when a story creates one, and return concrete
handoff evidence. Independent verification will decide pass/fail.

## Item

```
{{ args.item }}
```

## Prior drive result

```
{{ args.drive_result }}
```

## Required implementation drive

1. Open the implementation story with Studio MCP:
   - story: `{{ args.item.implementation_story }}`
   - harness: `{{ args.item.harness }}`
   - profile: `{{ args.item.profile }}`
   - ladder: `{{ args.item.harness_ladder }}`
   - trace: `{{ args.item.implementation_trace_path }}`
2. Drive it with natural operator text, using the implementation prompt:
   - `{{ args.item.implementation_prompt }}`
3. Capture the worktree, branch, changed files, and any story/MCP/usability friction.
4. Do not self-grade success. Verification happens after this room.

When done, submit JSON matching the acceptance schema. The submitted payload must
include:

```
{
  "status": "passed | partial | failed | skipped",
  "story": "{{ args.item.implementation_story }}",
  "trace_path": "{{ args.item.implementation_trace_path }}",
  "model": "{{ args.item.model }}",
  "profile": "{{ args.item.profile }}",
  "worktree": "<path if created>",
  "branch": "<branch if created>",
  "findings": ["..."],
  "summary": "what happened"
}
```

If you cannot prove the trace path or model, return `status: "partial"` with a
finding that explains the missing evidence.
