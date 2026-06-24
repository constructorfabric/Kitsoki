# Recipe: run work in the background and notify on completion

**Goal:** kick off long-running work without blocking the turn, then
react when it finishes.

Mark the `invoke:` as `background: true`. The call returns immediately
(bind its `job_id` if you want to track it); when the job terminates,
the orchestrator runs the `on_complete:` effects — typically reading
the result and posting a notification.

```yaml
hosts:
  - host.run

world:
  result:      { type: string, default: "" }
  last_job_id: { type: string, default: "" }

states:
  running:
    view:
      - prose: "Long operation in progress…"
    on_enter:
      - invoke: host.run
        with: { cmd: "long-running-script.sh" }
        background: true
        bind: { last_job_id: job_id }
        on_complete:
          - set: { result: "{{ world.last_job_result.stdout }}" }
          - say: "Job finished: {{ world.result }}"
    on:
      check_status:
        - target: running          # re-render while it runs

  done:
    view:
      - prose: "Result: {{ world.result }}"
    on:
      continue: [{ target: main }]
```

Completion arrives as an inbox notification on a later turn, not inline
— the user can keep interacting while the job runs. Test this with a
flow fixture and `advance_clock:` (see the
[flow test recipe](flow-test-with-cassette.md)).

**Reference**
- [`../stories/background-jobs/README.md`](../stories/background-jobs/README.md) — the full feature (authoring, runtime, testing, troubleshooting)
- [`../stories/background-jobs/recipes.md`](../stories/background-jobs/recipes.md) — more runnable background-job patterns
- [`../stories/state-machine.md`](../stories/state-machine.md) — effects: `background:`, `on_complete:`; the turn loop
