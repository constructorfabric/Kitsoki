You are driving ONE punch-list item through Kitsoki Studio MCP like a human operator.

This is real live dogfood. Do not use Claude. Use the requested Codex/GPT profile and
return only concrete handoff evidence; independent verification will decide pass/fail.

## Item

```
{{ args.item }}
```

## Required drive

1. Open the item story with Studio MCP:
   - story: `{{ args.item.story }}`
   - harness: `{{ args.item.harness }}`
   - profile: `{{ args.item.profile }}`
   - trace: `{{ args.item.trace_path }}`
2. Drive it with natural operator text, using the item prompt:
   - `{{ args.item.prompt }}`
3. Capture any story, MCP, routing, or usability friction as findings.
4. Do not claim implementation success. This drive is observation and handoff only.
5. If the target story was opened with the requested profile and explicit trace,
   and the trace contains the driven turns, use the requested item model in the
   submitted payload. The outer punch-list `host.agent.task` trace records the
   concrete driver model; the nested Studio trace may only expose profile/session
   metadata.
6. If a nested Studio MCP call times out, do not wait indefinitely for the
   underlying turn. Inspect status/trace once or twice, record whether late work
   is still writing, close or abandon the nested session if needed, and submit a
   `partial` handoff with the timeout/cancellation finding. The punch-list must
   keep moving rather than polling forever.

Use `status: "passed"` when the requested observation/handoff was completed,
all required handoff evidence is present, and either no implementation story is
configured or the observation supports continuing into that implementation.
Use `status: "partial"` when the drive itself was incomplete, required evidence
is missing, or the observation says an implementation item is stale,
not-reproducible, under-specified, or should not be attempted yet. Use
`status: "skipped"` when the item is clearly obsolete. Do not return `passed`
for a configured implementation item that should not proceed.

When done, submit JSON matching the acceptance schema. The submitted payload must
include:

```
{
  "status": "passed | partial | failed | skipped",
  "story": "{{ args.item.story }}",
  "trace_path": "{{ args.item.trace_path }}",
  "model": "{{ args.item.model }}",
  "profile": "{{ args.item.profile }}",
  "findings": ["..."],
  "summary": "what happened"
}
```

If you cannot prove the trace path, return `status: "partial"` with a finding
that explains the missing evidence.
