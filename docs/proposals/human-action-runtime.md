# Runtime: Human action runtime contract

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   ../human-action-workflows.md

## Why

Stories can already dispatch machine work through `host.agent.ask`,
`host.agent.decide`, `host.agent.task`, and `host.agent.converse`
(`docs/architecture/hosts.md`). Human-dependent work is scattered across
story-specific rooms, inbox notifications, ticket comments, and operator prompts.
That makes a person a side channel instead of a first-class workflow executor.

The runtime needs an explicit human-action contract so a story can declare a
person task, wait on it, trace it, mock it, and resume from it without knowing
whether the backend is GitHub, Jira, Linear, Slack, or a local file queue.
That contract also needs to work for roadmap and strategy calls, not only
ticket-sized tasks: prioritizing an initiative, approving an epic, or reviewing
a long-term plan is still a role-owned action with a typed request, an external
reference, a completion predicate, and a recorded result.

## What changes

Add a `host.human.*` family with a provider registry similar in spirit to the
agent registry but designed around external completion:

| Verb | Purpose | Result shape |
|---|---|---|
| `host.human.ask` | Ask a person/role a short question | `{status, answer, task_ref}` |
| `host.human.decide` | Request a structured verdict | `{status, submitted, task_ref}` |
| `host.human.task` | Assign an actionable human task and wait for done | `{status, submitted, task_ref, external_url}` |
| `host.human.converse` | Continue a workflow-scoped human thread | `{status, message, thread_ref}` |

`status` is one of `completed`, `pending`, `cancelled`, or `error`.
`pending` is not a failure: the orchestrator records the external reference and
the scheduler/webhook substrate resumes the state when the provider observes
completion.

The request may optionally include a compact `work_item` descriptor so planning
stories can attach the human action to a roadmap item:
`{id, level, parent, initiative, horizon, priority}`. The runtime treats it as
metadata for traceability and provider rendering; stories own the taxonomy.

When a task is created, the runtime also resolves a `task_room`: a persistent
chat scoped to the task ref. This room is the assistance surface for the
assigned person. Provider messages that explicitly ask for help can be routed
into that room, answered by the LLM under the task's context boundary, and
posted back through the provider.

## Impact

- **Code seams:** `internal/host/handlers.go`, a new `internal/human` or
  `internal/host/human_*` package, scheduler resume handling, host cassette
  matching, app loader validation for `humans:`
- **Vocabulary:** new host calls `host.human.ask`, `host.human.decide`,
  `host.human.task`, `host.human.converse`; optional app-level `humans:` role map
  and `work_taxonomy:` aliases
- **Stories affected:** none by default; adoption is opt-in
- **Backward compat:** existing stories and cassettes keep working unchanged
- **Docs on ship:** `docs/architecture/hosts.md` and
  `docs/architecture/human-actions.md`

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| host call | `host.human.task` | `{provider, role, title, body, acceptance, await, deadline, schema}` -> `{status, task_ref, submitted, external_url}` | Creates or updates an external human task |
| host call | `host.human.decide` | `{provider, role, prompt, options/schema, await}` -> `{status, submitted, task_ref}` | Structured human verdict |
| host call | `host.human.ask` | `{provider, role, question, await}` -> `{status, answer, task_ref}` | Human question/answer |
| host call | `host.human.converse` | `{provider, role, thread_ref, message}` -> `{status, message, thread_ref}` | Provider-backed thread |
| app config | `humans` | map of role -> provider identity | Keeps stories role-oriented |
| app config | `work_taxonomy` | optional aliases for levels/horizons | Lets projects map roadmap language without changing runtime |
| metadata | `work_item` | `{id, level, parent, initiative, horizon, priority}` | Optional trace/render context for roadmap work |
| metadata | `task_room` | `{room_id, scope_key, help_trigger, external_thread}` | Assistance room scoped to one human task |
| world key | caller chosen | `task_ref` / `submitted` / `status` | Bound explicitly like other host results |

## The model

```
story on_enter
  -> host.human.task
       -> provider.create_or_update
       -> status=completed -> bind result and continue
       -> status=pending -> scheduler wait / webhook resume
              -> provider.observe completion
                    -> synthetic completion turn
```

Interpretive work belongs to the person completing the task. Deterministic work
belongs to the runtime: rendering the request, creating the external task,
recording the reference, validating a submitted payload against a schema,
resuming the state, and replaying fixtures without the backend.

## Decision recording

Human actions should not reuse `agent.call.*` because the executor, latency, and
completion source are different. Add sibling events in the tracing slice:

- `human.call.start`
- `human.call.pending`
- `human.call.complete`
- `human.call.error`
- `human.call.cancelled`

Each event carries `call_id`, `verb`, `provider`, `role`, `external_ref`,
`external_url`, and the rendered request or observed completion payload needed
to reconstruct the workflow.

## Engine seams & invariants

- A `host.human.*` call must declare either a concrete `provider` or a role that
  resolves through app-level `humans:`.
- `schema` is required for `host.human.decide` and optional for
  `host.human.task`. When present, completion payloads are validated before
  binding into world.
- `await: true` means a `pending` result pauses/resumes through the scheduler.
  `await: false` returns immediately after creating/updating the external task.
- `task_room` creation is idempotent by `task_ref`; repeated polls or replays
  must not create duplicate help rooms.
- Task-help turns use the task's declared `allowed_context`. They should not
  inherit unrestricted agent workbench scope.
- The provider must be injectable. Tests can register an in-memory provider
  without shelling out or touching GitHub.

## Backward compatibility / migration

This is additive. Existing `needs_human` rooms, operator-ask, inbox rows, and
ticket adapters continue to work. Story authors migrate only places that need a
durable, externally-managed human dependency.

## Tasks

```
## 1. Contract
- [ ] 1.1 Define provider interface and request/response structs.
- [ ] 1.2 Register `host.human.task` and `host.human.decide`.
- [ ] 1.3 Add app-level `humans:` role resolution and load-time errors.
- [ ] 1.4 Add await/pending integration with scheduler resume.
- [ ] 1.5 Resolve/create `task_room` for human tasks and expose it in results.

## 2. Verification
- [ ] 2.1 Unit tests for completed, pending, cancelled, error, and schema-invalid results using an injected provider.
- [ ] 2.2 Flow fixture where `host.human.task` completes immediately from cassette.
- [ ] 2.3 Flow fixture where `host.human.task` returns pending and a synthetic completion turn resumes the story.
- [ ] 2.4 Flow fixture where a task-help turn resolves the same task room and does not complete the task.

## 3. Adopt + document
- [ ] 3.1 Document the host family and provider contract.
- [ ] 3.2 Migrate shipped design into docs and trim/delete this proposal.
```

## Verification

All default tests use fake providers or cassettes. No default test reaches a
real tracker, posts a GitHub issue, or waits on a live webhook.

## Open questions

1. **Provider package name:** `internal/human` vs. `internal/host/human`.
   *Lean: `internal/human` for the interface and fakes, `internal/host` for host
   adapters.*
2. **Minimum v1 verbs:** implement `task` and `decide` first, or all four
   verbs. *Lean: `task` and `decide`; keep `ask` and `converse` documented as
   reserved until a story needs them.*

## Non-goals

- A live GitHub implementation; that is the next slice.
- A new UI board; pending tasks reuse scheduler/inbox/runstatus first.
