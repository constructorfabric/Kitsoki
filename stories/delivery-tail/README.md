# delivery-tail — shared neutral delivery tail

The reusable integrate → verify → cleanup → report tail imported by
[ship-it](../ship-it/) and [bugfix](../bugfix/) (and any future story
that needs a lost-work-safe integrate + independent re-verify pipeline).

This story is **never run standalone as a root session**. It is always
entered at `integrate` by a parent story that has already run its maker
and seeds `worktree_path` + `workspace_branch` + `gate_command` via
`world_in`. It exits `@exit:shipped` (requires `shipped_sha`) or
`@exit:needs-human` (requires `last_error`).

## Pipeline

```
integrate ─▶ verify ─▶ cleanup ─▶ report ─▶ @exit:shipped
   │             │         │
   └─────────────┴─────────┴──────────────▶ @exit:needs-human (last_error)
```

All rooms are autonomous (no human interaction required): each fires a
`host.run` call on_enter, binds the outcome, and emits an internal
routing intent. No operator intents except `look` (re-render).

## Entry state

`integrate` — entered directly by the parent import (`entry: integrate`).

## Exits

| Name | Description | `requires:` |
|---|---|---|
| `shipped` | integrate clean, re-verify GREEN on merged commit, cleanup done. | `shipped_sha` |
| `needs-human` | any failure: integrate conflict/build-fail, verify RED, cleanup refused. Carries the real error. | `last_error` |

## World-in contract (parent must seed these)

| Key | Type | Default | Description |
|---|---|---|---|
| `worktree_path` | string | `""` | The maker worktree (branch is checked out here). |
| `workspace_branch` | string | `""` | The maker branch name. |
| `gate_command` | string | `""` | The deterministic gate command; re-run IDENTICALLY on the merged commit. |
| `base_branch` | string | `"main"` | Integration branch. |
| `main_worktree_path` | string | `"."` | Where integrate/verify/cleanup run. |
| `workspace_id` | string | `""` | Worktree identity (`.worktrees/<id>`). |
| `build_check_disabled` | bool | `false` | Skip build check after integrate. |
| `brief` | string | `""` | Displayed in the report read-out. |

## World-out keys (available to the parent via alias prefix)

When imported as alias `tail`, parent reads `world.tail__<key>`:

| Key | Type | Description |
|---|---|---|
| `shipped_sha` | string | The merged commit SHA (set only after green re-verify). |
| `last_error` | string | The real failure reason (on any needs-human arc). |
| `integrate_outcome` | string | `integrated` \| `conflict` \| `build_fail` \| `error` |
| `integrated_sha` | string | The merged commit SHA (bound by integrate). |
| `verify_ok` | bool | `true` (GREEN) \| `false` (RED). |
| `cleanup_outcome` | string | `cleaned` \| `refused` |

## Flows (no-LLM)

| Flow | Proves |
|---|---|
| `happy_path` | integrate (clean) → verify (green on merged commit) → cleanup → `@exit:shipped` |
| `e2e_mocked_maker` | full host-call sequence integrate → re-verify → cleanup → shipped; maker mocked via world-in seed |
| `integrate_conflict` | unresolvable conflict → `@exit:needs-human` (no swallowed success) |
| `verify_red_on_main` | branch green but gate RED on the merged commit → `@exit:needs-human` (trust-the-gate) |
| `cleanup_refused` | cleanup failure → `@exit:needs-human` |

```
kitsoki test flows stories/delivery-tail/app.yaml
```

## Importing

```yaml
imports:
  tail:
    source: ../delivery-tail
    entry: integrate
    world_in:
      worktree_path:       "{{ world.worktree_path }}"
      workspace_branch:    "{{ world.workspace_branch }}"
      gate_command:        "{{ world.gate_command }}"
      base_branch:         "{{ world.base_branch }}"
      main_worktree_path:  "{{ world.main_worktree_path }}"
      workspace_id:        "{{ world.workspace_id }}"
      build_check_disabled: "{{ world.build_check_disabled }}"
      brief:               "{{ world.brief }}"
    exits:
      shipped:
        to: "@exit:shipped"
        set:
          shipped_sha: "{{ world.tail__shipped_sha }}"
          status:      shipped
      needs-human:
        to: "@exit:needs-human"
        set:
          last_error: "{{ world.tail__last_error }}"
          status:     needs-human
```

## See also

- [`stories/ship-it/`](../ship-it/) — the single-brief delivery loop (configure + maker + tail).
- [`stories/bugfix/`](../bugfix/) — imports delivery-tail for direct-ship.
- [`docs/stories/delivery-loop.md`](../../docs/stories/delivery-loop.md) — the shipped delivery-loop story stack.
