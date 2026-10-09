# Story: Task-scoped assistance rooms

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   story
**Epic:**   ../human-action-workflows.md

## Why

A person assigned a human task often needs help before they can complete it.
They may need the LLM to summarize the relevant design, explain why the task was
assigned, draft a response, compare options, inspect linked evidence, or propose
next steps. If the assigned GitHub issue/comment is only a status mailbox, the
person has to leave the task context to ask for help, and Kitsoki loses the
traceable connection between the question, the answer, and the task decision.

The assigned ticket/comment chain should therefore be a room scoped to the task:
the human can ask for LLM help from the same place they are doing the work, and
the assistance stays bounded by that task's context.

## What changes

Each `host.human.task` creates or resolves a task-scoped room:

```yaml
task_room:
  app: "{{ app.id }}"
  room: "human_task"
  scope_key: "{{ task_ref.provider }}:{{ task_ref.id }}"
  title: "{{ title }}"
  lane: help
```

The room uses the existing persistent chat substrate (`host.chat.resolve`,
`host.chat.drive`) and room-chat lane model (`internal/roomchat`): help is
read-only by default and can use `host.agent.ask` / `host.agent.converse` with a
prompt that includes only the task, its parent work item, linked artifacts, and
allowed trace evidence.

For GitHub, the provider maps the issue/comment chain to that room. A reply such
as `@kitsoki help summarize the acceptance criteria` becomes an inbound room
turn. Kitsoki posts the LLM answer back to the same issue/comment thread and
records the room turn in the trace. Ordinary comments that do not invoke help
remain human evidence or completion material.

## Impact

- **Story files:** a reusable task-room prompt/room, or a dev-story/deliver room
  adopted by the human-action stories
- **Runtime dependency:** uses `host.chat.*`, roomchat lanes, the human-action
  runtime, GitHub backend, and tracing slice
- **Vocabulary:** `task_room`, `help_trigger`, `scope_key`, `room_id`,
  `external_thread`
- **Docs on ship:** `docs/architecture/human-actions.md` and a story guide for
  task-scoped assistance

## Room contract

| Field | Meaning |
|---|---|
| `task_ref` | provider-neutral task identity from `host.human.task` |
| `room_id` | Kitsoki persistent chat id backing help for the task |
| `scope_key` | stable key, usually `<provider>:<external-id>` |
| `external_thread` | provider thread/comment/issue URL |
| `parent_work_item` | optional roadmap/decomposition item context |
| `allowed_context` | docs, artifacts, trace refs, and repo paths the room may read |

The room is not a general agent escape hatch. It is constrained to the task and
its declared context. If a human asks for work outside that boundary, the room
should explain the boundary or ask the workflow owner to widen scope through a
recorded decision.

## GitHub interaction

V1 GitHub behavior:

1. `host.human.task` creates the issue/comment and includes a short help footer:
   "Reply `@kitsoki help <question>` for task-scoped assistance."
2. The GitHub provider stores `{task_ref, room_id, issue/comment ref}`.
3. A webhook/poll sees a matching help reply and resolves the room by
   `scope_key`.
4. `host.chat.drive` runs the help turn with the task-scoped prompt.
5. Kitsoki posts the answer back as a comment/reply and records the trace event.

The room should also work without GitHub: a TUI/web operator can open the same
task room directly from the pending human-action row.

## Tasks

```
## 1. Room substrate
- [ ] 1.1 Define task-room request/response fields on `host.human.task`.
- [ ] 1.2 Resolve/create a `host.chat` thread keyed by task ref.
- [ ] 1.3 Add a task-scoped help prompt that limits context to declared inputs.

## 2. GitHub bridge
- [ ] 2.1 Add help footer and room metadata to GitHub task materialization.
- [ ] 2.2 Route `@kitsoki help ...` comments into the task room.
- [ ] 2.3 Post the LLM answer back to the same GitHub thread.

## 3. Verification
- [ ] 3.1 No-LLM flow: human task created, help comment arrives, canned room answer posts back, task remains pending.
- [ ] 3.2 No-LLM flow: help answer cites task context and does not expose unrelated repo scope.
- [ ] 3.3 Document task-scoped assistance; migrate shipped details and trim/delete this proposal.
```

## Verification

Default tests use host/chat cassettes and fake GitHub comments. They should not
call a live LLM or GitHub. The fixture should prove that task help is scoped to
the task ref and does not complete the task unless the completion predicate is
also satisfied.

## Open questions

1. **Trigger syntax:** `@kitsoki help` vs. every comment as a possible turn.
   *Lean: explicit `@kitsoki help` so normal human discussion is not hijacked.*
2. **Room lane:** reuse `LaneHelp` or add `LaneHumanTask`. *Lean: use the help
   lane with a task-specific room/scope key first; add a lane only if UI needs a
   distinct treatment.*
3. **Write permissions:** can the helper draft edits or only advise? *Lean:
   read-only in v1; a later explicit human/owner approval can dispatch an agent
   task if the person asks for implementation.*

## Non-goals

- Turning every issue comment into an agent conversation.
- Letting task-help bypass the task's declared scope, completion predicate, or
  approval gates.
