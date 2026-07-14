# Tracing: Human action tracing and replay

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tracing
**Epic:**   ../human-action-workflows.md

## Why

Human actions must be inspectable and replayable for the same reason agent
actions are: when a workflow waits on a person, the trace should answer what was
asked, who or what backend owned it, where it lived externally, when it became
done, and what payload resumed the story. For roadmap work, it should also show
which goal, initiative, epic, change, decision, or gate the human action
affected. Without that, human work becomes a side channel that cannot be audited,
mocked, prioritized, or debugged.

## What changes

Add a human-action lifecycle event family and make host cassettes able to model
pending and completion. The trace should distinguish:

- dispatch of a human request
- creation/update of an external task
- pending wait
- observed completion
- cancellation/error
- task-scoped help turns and answers

Replay treats these events as annotations unless a synthetic completion turn is
being reconstructed. State/world reconstruction remains driven by existing
world updates and scheduler completion events.

## Impact

- **Code seams:** `internal/store/event.go`, JSONL event writers, runstatus
  timeline parsing, host cassette matching, scheduler completion trace rows
- **Vocabulary:** new `human.call.*` event family
- **Stories affected:** only stories using `host.human.*`
- **Backward compat:** existing traces and cassettes keep working
- **Docs on ship:** `docs/tracing/` or `docs/architecture/human-actions.md`

## Event vocabulary

| Event | When | Required fields |
|---|---|---|
| `human.call.start` | before provider dispatch | `call_id`, `verb`, `provider`, `role`, `state`, `request`, `work_item?` |
| `human.call.pending` | provider created/updated task but completion is outstanding | `call_id`, `task_ref`, `external_url`, `deadline` |
| `human.call.complete` | completion observed and validated | `call_id`, `task_ref`, `submitted`, `actor`, `duration_ms` |
| `human.call.error` | provider/runtime error | `call_id`, `error`, `task_ref?` |
| `human.call.cancelled` | workflow/operator cancels the wait | `call_id`, `task_ref`, `by`, `reason` |
| `human.help.requested` | a task-scoped help message is routed into the room | `task_ref`, `room_id`, `actor`, `message_ref`, `question` |
| `human.help.answered` | Kitsoki posts the help answer back to the task thread | `task_ref`, `room_id`, `message_ref`, `answer_ref`, `duration_ms` |

`work_item` is optional but should be present for planning/decomposition calls.
It carries compact taxonomy fields such as `{id, level, parent, initiative,
horizon, priority}` so roadmap decisions can be rolled up without scraping
provider-specific issue text.

## Replay and cassettes

Cassettes need to support two shapes:

1. Immediate completion:

   ```yaml
   host.human.task:
     data:
       status: completed
       task_ref: { provider: github, id: "owner/repo#123" }
       submitted: { verdict: approved }
   ```

2. Pending plus synthetic completion:

   ```yaml
   host.human.task:
     data:
       status: pending
       task_ref: { provider: github, id: "owner/repo#123" }
       external_url: "https://github.com/owner/repo/issues/123"

   scheduler.completed:
     data:
       call_id: human-1
       status: completed
       submitted: { verdict: approved }
   ```

The exact cassette encoding can follow the existing host-cassette style, but it
must not require a live webhook or a real clock to exercise the resume path.

## Runstatus surface

Runstatus should render a pending human action as active work, separate from an
agent call. The useful operator fields are role, provider, title, external link,
age/deadline, and current status. This can start as timeline rows and an inbox
work item before growing into a project board.

## Tasks

```
## 1. Events
- [ ] 1.1 Add `human.call.*` event kinds and payload structs.
- [ ] 1.2 Emit start/pending/complete/error/cancelled from `host.human.*`.
- [ ] 1.3 Emit task-help requested/answered events.
- [ ] 1.4 Include provider refs and submitted payloads without leaking unrelated external content.

## 2. Replay
- [ ] 2.1 Extend cassette matching for immediate completed human calls.
- [ ] 2.2 Add a no-LLM flow for pending -> synthetic completion resume.
- [ ] 2.3 Add a no-LLM flow for task-help request -> answer while the task remains pending.
- [ ] 2.4 Ensure trace replay remains deterministic without contacting the backend.

## 3. Surfaces
- [ ] 3.1 Add runstatus timeline rows for human action events.
- [ ] 3.2 Add runstatus/chat links for task-scoped help rooms.
- [ ] 3.3 Add inbox/work-item rows for pending human actions.
- [ ] 3.4 Document the event family and trim/delete this proposal.
```

## Verification

Verification is flow-first and fixture-backed. The key regression is a story
that pauses on `pending`, receives a recorded completion event, resumes exactly
once, and binds the submitted payload.

## Open questions

1. **Event naming:** `human.call.*` vs. `person.call.*`. *Lean:
   `human.call.*`, because the product language is human action.*
2. **Raw external payload storage:** inline full GitHub event vs. digest +
   artifact ref. *Lean: compact inline normalized payload, artifact ref for raw
   webhook body when needed.*

## Non-goals

- A new analytics dashboard.
- Replaying external GitHub webhook delivery itself; replay starts at the
  normalized observed completion event.
