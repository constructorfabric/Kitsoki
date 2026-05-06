# Proposal — Wire the existing Background-Jobs subsystem into orchestrator + TUI

> **Implemented.** See [docs/background-jobs/](docs/background-jobs/README.md) for the canonical guide.

**Status:** Draft. Authored from the cyber-repo `devstory` story consumer
side, where a long-running `build_plan` action (LLM-driven, 30-90s)
blocks the entire room while it runs.

**TL;DR:** the `internal/jobs/` package is ~70% built — schema, store,
scheduler interface, lifecycle types, notification table, dedup index,
clarification semantics — but **it isn't wired anywhere**.  YAML can
declare `background: true` on an effect today and hally silently ignores
it.  The TUI has no notification panel.  This proposal connects the
parts.

---

## 1. What's already in hally

### `internal/jobs/`

- `Scheduler` interface (`Submit`, `Cancel`, `Heartbeat`, `Subscribe`).
- `JobSpec` / `JobStatus` types matching design.md §4.
- `Store` over SQLite with the `jobs` and `notifications` tables exactly
  as specified in design.md §4.2 (incl. dedup index, snooze semantics,
  retry counter, clarification answer column).
- Goroutine-per-job supervisor with stale-job recovery
  (`error="process_died_mid_job"` on startup scan).
- Test coverage for the store + scheduler in isolation.

### `internal/app/types.go`

- `Effect.OnError string` — wired
- `ProposalExecute.Background bool` — parsed, **not consumed**
- `ProposalExecute.OnComplete []Effect` — parsed, **not consumed**
- `Effect.Invoke / Effect.With / Effect.Bind` — wired (synchronously)

### What this means

The data plane is done.  The integration plane is missing.  Today an
author can write:

```yaml
on_enter:
  - invoke: host.run
    with:    { cmd: "python3 long_thing.py" }
    background: true              # ignored
    on_complete:                  # ignored
      - say: "long thing done"
```

…and hally will run `long_thing.py` synchronously, blocking the room
until exit.  Notifications are never posted.  No panel renders them.

---

## 2. Three connected pieces

### 2.1  Orchestrator dispatch  *(~150 LoC; foundation)*

When the orchestrator processes an effect whose `Invoke` is non-empty:

- **Today:** call `host.Registry.Invoke(ctx, name, args)` synchronously,
  bind the result, fire `on_error` on non-nil error.
- **Proposed:** if the effect has `background: true`, call
  `scheduler.Submit(JobSpec{...})` instead.  The `bind:` map is rewritten
  on the fly to accept the placeholder fields:
  - `job_id` → bound to `<key>` if the YAML maps it
  - else default-bind to `world.last_job_id`

  Then post a notification record with `severity: info`,
  `title: "Job submitted: {kind}"`,
  `teleport.state: <current state>` so re-opening the inbox returns
  the user to where the job was launched.

When the job terminates (running → done|failed|cancelled), the
supervisor:

1. Updates the `jobs` row.
2. Posts a notification with `severity: success|error` and the
   originating proposal/job refs.
3. Runs the effect's `on_complete:` block in the **origin state's**
   context.  on_complete effects are normal effects (`set`, `say`,
   `invoke`, `emit`); they MUST NOT have `background: true` themselves
   (validated at load time).

The `on_complete` block can include a transition target via a special
`on_complete.go: <state>` shorthand if the room wants to auto-advance
when the job completes (e.g. `standup_plan_building` → `standup_plan`
once the build finishes).

### 2.2  TUI — add an Inbox panel  *(~250 LoC; UX)*

Today: two regions — transcript on the left, `Actions` panel on the right.

Proposed: Inbox panel below Actions, same right column:

```
+--------------------+
| Actions            |
+--------------------+
| 1. build plan      |
| 2. show plan       |
| 3. summarize       |
| 4. post to slack   |
| ...                |
+--------------------+
| Inbox      ●●○ 2 / 5   <- unread count + total
+--------------------+
| ✓ build_plan done   |
|   (3 m ago)         |
| ⚠ deploy needs      |
|   confirmation      |
|   (action required) |
| ⋯ test suite        |
|   running… 47%      |
+--------------------+
```

- Subscribed to `jobs.Store` via a `tea.Cmd` ticker (default 2s; bumped
  to 200ms while at least one job is `running`).
- Selecting an inbox item by number teleports to its
  `notification.teleport_state` with `teleport_slots` applied — already
  in the schema, just needs wiring.
- `action_required` items render a single-line banner above the input
  line: `[enter] open · [esc] later`.  Banner is consumed by an
  affirmative reply, snoozed by escape.
- Number prefixes share keyspace with Actions: `1-9` are Actions,
  `i1`-`i9` (or arrow-down past the actions) reach Inbox items.

Three default views ship:

- **Hidden** — when there are zero jobs and zero notifications, the
  Inbox panel collapses to a single-line `Inbox: empty`.
- **Compact** — default; latest 5 unread; one-line each.
- **Expanded** — toggle with `?inbox` or some key chord; full body of
  the latest 20.

### 2.3  Status-line badge  *(~30 LoC)*

A persistent line above the input shows the current Inbox summary even
when the panel is collapsed:

```
[main]  inbox: 2 unread · 1 action_required          turn 27
```

This is design.md §4.1 — already in HALLY-GAPS as a known gap from
the devstory story.  Cheap to ship alongside the panel.

---

## 3. YAML surface

No new fields.  The existing `background: true` and `on_complete:` on
`ProposalExecute` get extended to `Effect` so `on_enter` can use them too:

```go
type Effect struct {
    // existing fields...

    // Background, when true, dispatches Invoke as a job and binds
    // job_id (or default last_job_id) instead of running synchronously.
    Background bool `yaml:"background,omitempty"`

    // OnComplete fires after the job terminates.  Effects in this list
    // run in the originating state's context.  Cannot itself contain
    // background: true (load-time validation).
    OnComplete []Effect `yaml:"on_complete,omitempty"`
}
```

Author example (the actual devstory consumer):

```yaml
standup_plan_building:
  view: |
    Generating today's plan…
    {{ world.daily_plan_summary }}
  on_enter:
    - invoke: host.run
      with:
        cmd: "python3 stories/devstory/scripts/standup/build_plan.py"
      bind:
        last_job_id: job_id
      background: true
      on_complete:
        - set:
            daily_plan: "{{ job.result.stdout }}"
        - say: "Today's plan is ready ({{ job.result.summary }})"
        - go: standup_plan
```

---

## 4. Notification posting from a host handler

Already supported by `jobs.Store.PostNotification()` — handlers can post
mid-flight (e.g. progress milestones) via the context-injected store
handle, which is already in `host.Handler`'s context per
`internal/host/host.go:108`.  No code change needed; document the
pattern so room authors know about it.

---

## 5. Clarifications (already wired in store)

`jobs.Store` already supports `awaiting_input` status with a
clarification schema column.  Wire is straightforward:

1. Handler posts a clarification via `Store.PostClarification(jobID, schema)`.
2. Notification with `severity: action_required` is posted automatically.
3. Inbox panel highlights it.
4. User selects → teleports to a `clarifying` sub-state in the origin
   room with the proposal rehydrated and a schema-driven form
   (or, v1, a single text input bound to `slots.answer`).
5. Room's existing `answer_clarification` intent submits via
   `Store.AnswerClarification(jobID, value)`.
6. Job resumes from `running`.

---

## 6. Determinism & replay

- Job submit + completion + notification post are recorded as events in
  the existing event log.
- `hally replay` substitutes the recorded result for the host call
  (this works today for synchronous calls; same machinery applies).
- Flow tests need a way to advance virtual time so a `background: true`
  effect's `on_complete` can be exercised without real wall-clock wait.
  The previous proposal logged this as a separate gap (virtual clock for
  testrunner).  When that lands, a flow YAML can do:

  ```yaml
  - intent: { name: build_plan }
    expect_state: standup_plan_building
    advance_clock: 30s   # processes any background-job ticks
  - expect_state: standup_plan
    expect_world:
      daily_plan_total: 5
  ```

---

## 7. Implementation sketch (concrete file list)

- `internal/orchestrator/`:
  - `effects.go` — branch on `effect.Background`; submit to scheduler.
  - `oncomplete.go` (new) — apply `on_complete` effects when scheduler
    notifies completion; emit through the same trace path so
    `--trace-pretty` shows the post-job effect chain.
- `internal/jobs/`:
  - small `Notify(JobID, severity, title, body, teleport)` helper
    layered on `Store.PostNotification` — saves call sites at
    submit/done/failed.
  - `Subscribe(sessionID) <-chan Update` for the TUI ticker.
- `internal/app/`:
  - extend `Effect` struct; extend `effect_test.go` schema fixtures.
  - load-time validation that `on_complete` doesn't contain
    `background: true`.
- `internal/tui/`:
  - new `inbox.go` rendering the panel.
  - layout split between `menu.go` (Actions) and `inbox.go` (Inbox).
  - status-line badge in the existing turn-counter row.
  - tea.Cmd ticker, debounced (only re-render when notifications
    delta is non-empty).
- `cmd/hally/run.go`:
  - construct `jobs.Scheduler`, pass to orchestrator.
  - inject `jobs.Store` handle into `host.Registry`'s context.
- Tests:
  - `internal/orchestrator/background_test.go` — submit + on_complete + bind.
  - `internal/tui/inbox_test.go` — rendering with mock store, action-required interrupt.
  - `internal/jobs/integration_test.go` — full submit → done → notify
    → mark-read cycle.

Total: ~1,200 LoC of net-new code, mostly in two files (orchestrator
dispatch + TUI panel).  Most of the rest is plumbing.

---

## 8. Suggested rollout

1. **Effect.Background dispatch** (orchestrator + Effect struct
   extension).  Smallest unit; ships background semantics without
   any UX.  Authors can use it; results bind to world; on_complete
   fires.  No panel yet — users see jobs only via world slots.

2. **TUI Inbox panel + status-line badge.**  Now jobs become visible
   without changing any room YAML.

3. **Clarification round-trip wiring.**  Last because requires more
   YAML thought (clarifying sub-states pattern).

Each step is independently shippable and useful.

---

## 9. Why now

The cyber-repo `devstory` story has at least three actions that should
be background jobs but block synchronously today:

- `build_plan` — claude takes 30-90s investigating Jira/Bitbucket/git.
- Terminal `accept` for any command that takes >5s.
- Test Room `run_suite` (go test ./...) — minutes of blocking.
- Deploy Room `trigger_deploy` + `watch` — minutes.
- Bug Fix Room `run` — full bug-fix.py pipeline, 10+ minutes.

Each of these has a `background: true` flag in the YAML the author
already wrote, expecting it to work.  None do.  Until this proposal
lands every long-running action is a hard block, the user can't
multitask, and the room author can't legitimately model long work.

Conversely, once it lands, the consumer side gets cheap wins:
existing rooms just toggle `background: true` and add an `on_complete`
block.  No further consumer changes required.

---

## 10. Non-goals

- Distributed scheduling (still goroutine-per-job in-process).
- Job priorities / QoS.
- Cross-session jobs (jobs stay session-scoped per design §4.2).
- Replacing `host.run` — synchronous path remains the default.
- Real-time streaming of job stdout to the inbox panel.  That's the
  separate "streaming host results" gap (§7.1 in the cyber-repo
  HALLY-GAPS).  This proposal makes "submit and notify on done" work;
  streaming is a follow-up.
