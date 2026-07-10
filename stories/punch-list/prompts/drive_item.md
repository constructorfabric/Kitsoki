You are driving ONE punch-list item through Kitsoki Studio MCP like a human operator.

This is real live dogfood. Use the requested model policy and return only concrete
handoff evidence; independent verification will decide pass/fail.

## Item

```
{{ args.item }}
```

## Required drive

1. Open the item story with Studio MCP's `session.new`:
   - story: `{{ args.item.story }}`
   - harness: `{{ args.item.harness }}`
   - profile: `{{ args.item.profile }}`
   - ladder: `{{ args.item.harness_ladder }}`
   - trace: `{{ args.item.trace_path }}`
   - If the item carries a `world_in` object, pass it verbatim as `session.new`'s
     `initial_world` argument:
     ```
     {{ args.item.world_in }}
     ```
     This seeds the target story's world (e.g. `ticket_id`, `ticket_title`,
     `tickets_root`) BEFORE its first on_enter runs, so it self-provisions
     (workspace/branch/workdir, and — for stories that fetch a ticket record —
     its `iface.ticket.get`) the same way it would for an operator who already
     picked a real ticket. You SHOULD still pass `initial_world` — it is the
     clean, explicit path. If `world_in` is absent, proceed with the free-text
     prompt only (older/simpler manifests may not carry one — punch-list itself
     is generic and never requires this field).
   - Deterministic backstop (no action needed from you): the studio server now
     ALSO injects this seed into your `session.new` automatically, keyed by the
     parent session lineage, EVEN IF you forget `initial_world`. Your explicit
     `initial_world` still wins on any conflicting key (the backstop only fills
     gaps), so the outcome is identical whether or not you pass it. The loop no
     longer strands on an empty `ticket_id` just because the arg was omitted —
     but passing it remains the correct, legible thing to do.
2. Drive it with natural operator text, using the item prompt:
   - `{{ args.item.prompt }}`
   - IMPORTANT: send the item prompt to the target session's FIRST turn
     essentially VERBATIM — do not paraphrase or drop its leading sentence.
     The prompt is authored to LEAD with an explicit ticket directive (e.g.
     `Work ticket TKT-001 titled "Add a refresh endpoint". <brief>`). The
     target story's `idle` room extracts `ticket_id` (and, best-effort,
     `ticket_title`) from that EXACT leading phrase via a semantic-router
     template — this is the ONLY reliable path when `initial_world` above
     did not actually take (live drives have shown the maker opening the
     session without it despite step 1's instruction). Rewording, summarizing,
     or leading with your own framing instead of the literal ticket directive
     will strand the session with an empty `ticket_id` and nothing to
     self-provision from, exactly like skipping `initial_world`.
3. Capture any story, MCP, routing, or usability friction as findings.
4. Do not claim implementation success. This drive is observation and handoff only.
5. If the target story was opened with the requested profile and explicit trace,
   and the trace contains the driven turns, use the requested item model in the
   submitted payload. When `harness` is `ladder`, that item model is the
   cheap-first starting rung; the outer punch-list `host.agent.task` trace records
   the concrete driver model and ladder rung used for the actual attempt.
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
