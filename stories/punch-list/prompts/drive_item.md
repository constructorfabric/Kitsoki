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

If you cannot prove the trace path or model, return `status: "partial"` with a
finding that explains the missing evidence.
