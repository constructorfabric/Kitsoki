# Recipes

Runnable patterns for common background-job use cases.

---

## Long-running shell command with progress

**Pattern:** a handler reports progress via `sched.Heartbeat`; `on_complete:`
reads the final result from `last_job_result`.

The handler captures the scheduler from the outer scope (it is registered at
process start when the scheduler is already available).

### Handler (Go)

```go
// Register at process start, capturing sched (a jobs.Scheduler).
reg.Register("host.run_with_progress", func(ctx context.Context, args map[string]any) (host.Result, error) {
    cmd, _ := args["cmd"].(string)
    jobID := host.JobContextFromContext(ctx).JobID  // injected by scheduler

    // Report progress every second for 5 seconds.
    for i := 1; i <= 5; i++ {
        host.ClockFromContext(ctx).Sleep(1 * time.Second)
        _ = sched.Heartbeat(jobID, map[string]any{"pct": i * 20})
    }

    out, err := exec.CommandContext(ctx, "sh", "-c", cmd).Output()
    if err != nil {
        return host.Result{Error: err.Error()}, nil
    }
    return host.Result{Data: map[string]any{"stdout": string(out)}}, nil
})
```

### YAML

```yaml
hosts:
  - host.run_with_progress

world:
  status: { type: string, default: "" }

states:
  building:
    view: "Building… {{ world.status }}"
    on_enter:
      - invoke: host.run_with_progress
        with:
          cmd: "make build"
        background: true
        bind:
          last_job_id: job_id
        on_complete:
          - set:
              status: >-
                {{ world.last_job_status == "done"
                   ? "Done: " + world.last_job_result.stdout
                   : "Failed" }}
```

---

## Slack-driven approval mid-job

**Pattern:** a handler pauses with `host.RequestClarification`; the user
provides approval via the `answer_clarification` intent.

### Handler (Go)

```go
reg.Register("host.deploy_with_approval", func(ctx context.Context, args map[string]any) (host.Result, error) {
    env, _ := args["env"].(string)

    // Ask the user for approval
    rawJSON, err := host.RequestClarification(ctx, jobs.ClarificationSchema{
        Prompt: fmt.Sprintf("Approve deployment to %s? (yes/no)", env),
        Fields: map[string]string{"approved": "bool"},
    })
    if err != nil {
        return host.Result{Error: err.Error()}, nil
    }

    var answer struct{ Approved bool }
    if err := json.Unmarshal([]byte(rawJSON), &answer); err != nil {
        return host.Result{Error: "bad answer: " + err.Error()}, nil
    }
    if !answer.Approved {
        return host.Result{Error: "deployment rejected by user"}, nil
    }

    // Proceed with deployment
    return host.Result{Data: map[string]any{"deployed": true, "env": env}}, nil
})
```

### YAML

```yaml
hosts:
  - host.deploy_with_approval
  - host.jobs.answer_clarification

intents:
  approve_deploy:
    title: "Approve deployment"
    slots:
      job_id: { type: string, required: true }
      approved: { type: bool, required: true }

states:
  deploying:
    view: "Deploying to {{ world.env }}…"
    on_enter:
      - invoke: host.deploy_with_approval
        with:
          env: "{{ world.env }}"
        background: true
        bind: { last_job_id: job_id }
        on_complete:
          - say: >-
              {{ world.last_job_status == "done"
                 ? "Deployed successfully."
                 : "Deployment failed: " + world.last_job_status }}

  deploying_clarifying:
    description: "Awaiting approval."
    view: |
      Job {{ world.last_job_id }} needs approval.
      Check the inbox for details.
    on:
      approve_deploy:
        - target: deploying
          effects:
            - invoke: host.jobs.answer_clarification
              with:
                job_id: "{{ slots.job_id }}"
                answer: '{"approved": {{ slots.approved }}}'
```

---

## Background test suite + post result to transport

**Pattern:** run a test command as a background job; `on_complete:` posts the
result via `host.transport.post` (the built-in transport bridge).

### YAML

```yaml
hosts:
  - host.run
  - host.transport.post

world:
  test_output: { type: string, default: "" }
  test_status: { type: string, default: "" }

states:
  ci_running:
    view: "Running tests… (job {{ world.last_job_id }})"
    on_enter:
      - invoke: host.run
        with:
          cmd: "go test ./..."
        background: true
        bind: { last_job_id: job_id }
        on_complete:
          - set:
              test_output: "{{ world.last_job_result.stdout }}"
              test_status: "{{ world.last_job_status }}"
          - invoke: host.transport.post
            with:
              channel: "github-pr-comments"
              body: |
                Tests **{{ world.test_status }}**.
                ```
                {{ world.test_output }}
                ```
```

> **Note:** `host.transport.post` requires a transport registry configured
> with a `github-pr-comments` channel. Wire it via
> `orchestrator.WithTransportRegistry(r)`.

---

---

## Chat-aware background turn

**Pattern:** A chat-aware Oracle invocation, dispatched as a background job
so the orchestrator (or `loop.py`) doesn't block waiting for Claude. The
chat lock serialises concurrent drivers; on completion the inbox shows a
chat-friendly notification ("Reply ready — <preview>") that teleports back
to the originating room with `world.last_job_result.answer` bound.

### YAML

```yaml
hosts:
  - host.chat.resolve
  - host.oracle.decide

states:
  phase_3_executing:
    on_enter:
      - invoke: host.chat.resolve
        with: { app: "bugfix", room: "phase_3", scope_key: "{{ world.ticket_key }}" }
        bind: { active_chat_id: "chat_id" }
      - invoke: host.oracle.decide
        with:
          chat_id: "{{ world.active_chat_id }}"
          prompt: prompts/03-fix-proposal.txt
          schema: schemas/03-fix-proposal.json
        background: true
        on_complete:
          - set: { phase_artifact: "{{ world.last_job_result.submitted }}" }
```

### What you get

- A persistent transcript per (room, scope_key) — re-running phase 3 for
  the same ticket appends to the existing chat.
- A singleton lock so a TUI attached to the same chat won't race with the
  orchestrator.
- An inbox notification with a 60-char preview of Claude's answer when
  the turn completes; clicking it teleports to `phase_3_executing` with
  `world.last_job_result` populated.

### Notes

- `host.oracle.converse` works the same way; use it for free-form Q&A rooms.
- `host.chat.resolve` is idempotent — calling it on every `on_enter` is
  cheap and correct.

---

## See also

- [`README.md`](README.md) — entry point and glossary.
- [`authoring.md`](authoring.md) — full YAML field reference.
- [`testing.md`](testing.md) — how to test these patterns with flow fixtures.
- [`troubleshooting.md`](troubleshooting.md) — common pitfalls.
