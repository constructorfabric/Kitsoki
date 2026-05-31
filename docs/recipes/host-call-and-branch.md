# Recipe: invoke a host call and branch on success vs error

**Goal:** call a `host.*` handler, bind its result into world on
success, and route to a visible error state on failure.

Use `invoke:` with templated `with:` arguments, `bind:` to copy result
fields into world, and `on_error:` to name the state to enter when the
handler returns an error. **Without `on_error:`, a failed host call is
the classic "silent bounce back to idle"** — always give side-effecting
calls an error arc.

```yaml
hosts:
  - host.run

states:
  checking_status:
    on_enter:
      - invoke: host.run
        with:
          cmd: "git status"
          cwd: "{{ world.workspace_root }}"
        bind:
          last_output: stdout      # world.last_output <- result.stdout
          last_code:   exit_code
        on_error: status_failed
    on:
      continue:
        - target: main

  status_failed:
    view:
      - prose: "Status check failed. Output:"
      - code: "{{ world.last_output }}"
    on:
      retry:    [{ target: checking_status }]
      continue: [{ target: main }]
```

`on_enter:` effects run when the state is entered, so this calls
`host.run` as soon as you reach `checking_status`. The `host.*`
contract (what each handler accepts and returns) lives in the hosts
reference.

**Reference**
- [`../architecture/hosts.md`](../architecture/hosts.md) — every built-in handler's input/output contract
- [`../stories/state-machine.md`](../stories/state-machine.md) — effects: `invoke`, `with`, `bind`, `on_error`, `on_enter`
- [`../stories/authoring.md`](../stories/authoring.md) — calling a host
