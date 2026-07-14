# Epic: Human action workflows

**Status:** Draft v1. No slices implemented yet.
**Kind:**   epic
**Slices:** 6 (0/6 shipped)

## Why

Kitsoki can model agent work as explicit `host.agent.*` calls: the story declares
the task, the runtime dispatches it, the trace records start/complete/error, and
flows can mock it without spending LLM budget. Human work is not represented with
the same force. Today a person-dependent step is usually an issue comment, a
story-specific `needs_human` room, an inbox row, or a ticket status convention.
Those are useful surfaces, but they do not give the workflow a single typed
operation that says: "this turn is blocked on a named human task, backed by a
pluggable tracker, and resume only when the task is done."

The missing concept matters most when planning and work decomposition produce
mixed work: some briefs should be handled by agents, some need a maintainer,
designer, reviewer, customer, operator, or stakeholder. The same pattern also
applies above the ticket level. Roadmaps, rollup epics, strategic initiatives,
goal walls, priority calls, and quarterly bets are work items too: they are
proposed, refined, reviewed, prioritized, decomposed, tracked, and completed by
people and agents together. Kitsoki should route all of those through the same
project graph, with the same replay, trace, mocking, and management standards.

## What changes

Add a first-class **human action** host family parallel to `host.agent.*`:

- `host.human.ask` opens a short blocking question for a person or role.
- `host.human.decide` requests a structured approval/verdict.
- `host.human.task` creates or updates an assigned human task, then waits for
  completion through a pluggable backend.
- `host.human.converse` maps a workflow-scoped discussion onto a backend thread.

Every assigned human task also gets a **task-scoped assistance room**. The
person can reply in the ticket/comment chain with a question or request for help
("summarize context", "draft a response", "what are the options?", "run the
analysis"), and Kitsoki routes that message into an LLM-backed room scoped to
that task ref. The room can read the task, its parent roadmap/decomposition
context, linked artifacts, and allowed trace evidence, but it does not escape
into unrelated project state unless the story grants that scope.

The v1 backend is GitHub Issues. A story can say "ask Alice to approve the
release", "assign docs review to the maintainer", "wait for design sign-off",
"prioritize this epic against the roadmap", or "propose a strategic initiative
with gates" without hard-coding GitHub. The GitHub adapter materializes the
request as an issue or issue comment, records the external id in world/trace,
and resumes the workflow when a poll or webhook observes the done condition.

Add a shared work taxonomy that scales from immediate tasks to long-horizon
portfolio planning. It starts from existing Kitsoki research and shipped shapes:
the PRD/design scout's `roadmap_fit` and overlap recommendations
(`stories/prd/prompts/prd_existing_state.md`,
`stories/dev-story/prompts/design_existing_state.md`), proposal epic/slice
lifecycle (`docs/proposals/templates/`), the decomposition manifest and DAG lint
(`docs/stories/deliver.md`), and the generalized-usage traceability
spine in `.context/generalized-usage-decomposition-overview.md`
(`goal -> wall -> change -> gate -> commit -> trace`).

Human actions are interpretive decisions by people, so the runtime records them
with the same discipline as agent calls. Replay does not call GitHub. Flow
fixtures and cassettes provide the created task, intermediate pending state, and
completion payload.

## Impact

- **Spans:** runtime, tracing, story
- **Net surface:** new `host.human.*` verbs, a `human` plugin/provider registry,
  GitHub Issues provider, trace events, cassette fixtures, a portfolio/roadmap
  taxonomy, and adoption paths in work decomposition/dev-story
- **Docs on ship:** `docs/architecture/hosts.md`,
  `docs/architecture/human-actions.md`, `docs/stories/dev-story.md` or the
  dev-story README, and the shipped work-decomposition/deliver docs

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | Human action runtime contract | runtime | Define `host.human.*`, provider interface, pending/completed semantics, and load-time rules | - | Draft | [`human-action-runtime.md`](human-action-runtime.md) |
| 2 | GitHub human action backend | runtime | Implement GitHub Issues as the first human-action provider using existing `gh` seams | 1 | Draft | [`github-human-action-backend.md`](github-human-action-backend.md) |
| 3 | Human action tracing and replay | tracing | Record human task lifecycle events and make cassettes/flows deterministic | 1 | Draft | [`human-action-tracing.md`](human-action-tracing.md) |
| 4 | Roadmap and portfolio work taxonomy | story | Model initiatives, epics, walls, changes, gates, and prioritization as traceable work items | 1, 3 | Draft | [`roadmap-portfolio-work.md`](roadmap-portfolio-work.md) |
| 5 | Task-scoped assistance rooms | story | Turn each human task's ticket/comment chain into a scoped LLM-help room | 1, 2, 3 | Draft | [`human-task-assistance-rooms.md`](human-task-assistance-rooms.md) |
| 6 | Human work in decomposition | story | Let planning/decomposition emit human briefs and coordinate them beside agent briefs | 1, 2, 3, 4, 5 | Draft | [`human-work-decomposition.md`](human-work-decomposition.md) |

## Sequencing

```
#1 runtime contract -> #2 GitHub backend -> #5 task assistance -> #6 decomposition adoption
          +-----------> #3 tracing/replay -+-> #4 roadmap taxonomy ----+
```

The runtime contract lands first because both the backend and cassette shape
need one vocabulary. Tracing can land in parallel with the GitHub backend once
the event payloads are named. Roadmap taxonomy can land once human calls and
trace events are named. Task-scoped assistance rooms land after the
runtime/backend can create task refs. Decomposition adoption comes last so it
consumes the real contract, roadmap taxonomy, and help-room behavior instead of
inventing story-local conventions.

## Shared decisions

1. **Human actions are not agent plugins.** They deliberately share the
   `ask` / `decide` / `task` / `converse` vocabulary with agents, but they use a
   separate provider registry because completion is external and may take hours
   or days.
2. **`task` is awaitable, not always synchronous.** A `host.human.task` call can
   return `pending` and schedule a resumable background wait. A flow fixture can
   still collapse it to an immediate completed result for deterministic tests.
3. **GitHub is the first backend, not the model.** The provider contract is
   ticket/task/thread shaped: create, update, observe, complete, cancel. GitHub
   Issues implements that contract; Jira, Linear, Slack, email, or an internal
   task board can be sibling providers later.
4. **Human completion is a recorded decision.** The trace must show who or what
   source completed the task, the external reference, the observed completion
   payload, and the story state resumed from it.
5. **No tests call live GitHub by default.** Unit tests inject providers, flow
   tests use cassettes, and webhook/polling integration tests are gated.
6. **Roadmap work uses the same lifecycle as code work.** Strategic initiatives
   are not a separate planning wiki. They are typed work items with owners,
   decisions, dependencies, gates, status, evidence, and trace records.
7. **The assigned ticket is an interaction surface, not just a mailbox.** A
   human task owns a scoped room. The person can ask the LLM for help inside the
   ticket/comment chain, and the answer is posted back through the same backend
   with the task ref, room id, and trace id recorded.

## Cross-cutting open questions

1. **Should v1 expose every verb?** `host.human.task` is the load-bearing verb;
   `ask`, `decide`, and `converse` may initially be thin aliases over task
   shapes. *Lean: define the family now, implement `task` and `decide` first.*
2. **What is the completion predicate?** GitHub can use labels, closed state,
   assignee, or a structured fenced block in a comment. *Lean: provider default
   is closed issue or `kitsoki:done` label; stories can override with a
   provider-specific predicate.*
3. **Who owns identity mapping?** Stories naturally talk about roles
   (`maintainer`, `designer`, `release_manager`), while backends need logins.
   *Lean: app-level `humans:` block maps roles to provider identities.*
4. **How visible should pending waits be in the existing inbox/job surfaces?**
   *Lean: pending human tasks should appear as scheduler jobs and inbox rows,
   but the authoritative state is the human-action event pair plus provider ref.*
5. **How much taxonomy belongs in v1?** *Lean: ship a small hierarchy
   (`goal|initiative|epic|change|task|decision|gate`) and let stories add
   domain-specific tags rather than hard-coding every PM framework.*
6. **How should a person invoke task-help from a ticket?** *Lean: replies that
   mention the app (`@kitsoki help ...`) enter the task room; ordinary comments
   stay as human evidence/completion material.*

## Non-goals

- Replacing `host.agent.*`; agents and humans remain separate executors.
- Building a full project-management UI in v1. The workflow/runtime contract
  comes first.
- Replacing the existing `ticket` interface. `iface.ticket` still searches,
  gets, comments, and transitions tickets; `host.human.task` creates an awaited
  dependency inside a running workflow.
- Calling real GitHub from automated tests.
- Implementing a full OKR/portfolio product before the traceable work-item model
  exists.
