# Runtime: GitHub human action backend

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   runtime
**Epic:**   ../human-action-workflows.md

## Why

The human-action contract needs one real backend to prove the abstraction. The
repo already has GitHub Issues ticket substrate in `internal/host/github.go`,
including `create`, `comment`, `comment_edit`, `transition`, and deterministic
`cliExec` injection. GitHub Issues is the right first backend because it is
already where project work and review requests commonly live, and it gives
humans a familiar async surface.

## What changes

Add a GitHub provider for `host.human.task` / `host.human.decide`. It maps a
human action to a GitHub issue or issue comment with a small fenced metadata
block that identifies the Kitsoki session, call id, role, expected completion
shape, and done predicate.

V1 supports two materialization modes:

| Mode | Use | GitHub action |
|---|---|---|
| `issue` | standalone human task | create or update an issue |
| `comment` | task attached to an existing issue/PR | post or edit a comment |

Completion is observed by polling in local runs and by webhook in deployed
GitHub-agent contexts. The provider reports `completed` when the configured
predicate is true.

The same GitHub issue/comment chain is also the human's help surface. A task
footer tells the assignee how to ask for assistance, for example
`@kitsoki help summarize the context`. Matching comments are routed into the
task-scoped assistance room and answered back on the same thread; non-help
comments continue to be treated as human discussion or completion evidence.

## Impact

- **Code seams:** reuse `internal/host/github.go`, `internal/host/cli_exec.go`,
  and GitHub-agent webhook/job surfaces where available; add GitHub provider
  under the human action registry
- **Vocabulary:** provider `github`, optional args `repo`, `target`, `mode`,
  `labels`, `assignee`, `done_label`, `done_state`, `help_trigger`
- **Stories affected:** opt-in only
- **Backward compat:** `host.gh.ticket` remains the ticket interface; this slice
  may call into shared helpers but does not change `iface.ticket`
- **Docs on ship:** human-actions architecture doc plus a GitHub provider
  subsection in `docs/architecture/hosts.md`

## Vocabulary changes

| Kind | Name | Shape | Notes |
|---|---|---|---|
| provider | `github` | `repo`, `mode`, `target`, `assignee`, `labels`, `done_label`, `done_state` | Implements human-action provider |
| task ref | `github_issue` | `{repo, number, url, comment_id?}` | Stored in trace/world as provider-neutral `task_ref` plus provider details |
| completion predicate | `done_label` | string | Default `kitsoki:done` |
| completion predicate | `done_state` | `open|closed` | Default allows closed issue as done |
| help trigger | `help_trigger` | string | Default `@kitsoki help`; routes comments into the task room |

## The model

```
host.human.task(provider=github, role=designer, await=true)
  -> resolve role -> GitHub login/team
  -> create issue or comment with kitsoki metadata
  -> return pending + task_ref
  -> poll/webhook observes label/comment/closed state
  -> return completed payload into scheduler resume
```

The provider is responsible for GitHub I/O. The runtime owns validation,
trace events, and replay behavior.

## Decision recording

The provider returns `task_ref` and observed completion data. The tracing slice
records it under `human.call.*` events. The GitHub provider should include:

- `repo`
- issue number
- comment id when applicable
- html URL
- actor/login that completed the task when observable
- labels/state or submitted structured payload that satisfied the predicate
- task-room id and comment id for any help request/answer round trip

## Engine seams & invariants

- GitHub calls go through `cliExec` or a provider-level interface so tests can
  inject results.
- Missing `gh`, missing auth, or bad repo returns a clean host `Result.Error`.
- The provider never assumes one GitHub account per role; `humans:` can map a
  role to a login, team mention, or raw assignee string.
- Help comments are explicit. The provider should not treat every issue comment
  as an LLM turn.
- Webhook completion must verify the event maps to a known pending task ref
  before resuming a session.

## Backward compatibility / migration

This is additive. It can share helpers with `host.gh.ticket`, but it should not
change the existing ticket provider's output contract or dev-story ticket
routing behavior.

## Tasks

```
## 1. Provider
- [ ] 1.1 Implement GitHub provider behind the human-action provider interface.
- [ ] 1.2 Support issue and comment materialization.
- [ ] 1.3 Add completion observation via injected poller.
- [ ] 1.4 Wire webhook completion in GitHub-agent contexts if the deployed substrate is present.
- [ ] 1.5 Add help footer, help-trigger parsing, task-room routing, and reply posting.

## 2. Verification
- [ ] 2.1 Unit tests with fake GitHub CLI/provider responses.
- [ ] 2.2 Flow cassette for create -> pending -> complete.
- [ ] 2.3 Flow cassette for create -> help comment -> answer posted -> still pending.
- [ ] 2.4 Gated live smoke that creates a throwaway issue only when explicitly requested.

## 3. Document
- [ ] 3.1 Document provider config, role mapping, and done predicates.
- [ ] 3.2 Migrate shipped details to docs and trim/delete this proposal.
```

## Verification

Default verification uses fake provider responses and recorded cassettes.
The live GitHub smoke is gated and should not run in ordinary `go test` or flow
validation.

## Open questions

1. **Issue or comment default:** standalone issue vs. comment on an existing
   tracker item. *Lean: issue when no `target` is supplied; comment when a
   `target` issue/PR exists.*
2. **Completion payload source:** parse a fenced block in a comment, infer from
   labels/state, or both. *Lean: both; structured block wins when present.*

## Non-goals

- Replacing `host.gh.ticket`.
- Supporting GitHub Projects fields in v1.
- Creating live GitHub issues from default tests.
