# dev-story — Design Notes

Living design document for the dev-story application and the hally platform additions it requires.  Companion to [dev-story.md](./dev-story.md) (the user-facing app description) and [design.md](./design.md) (the hally platform spec).

Status legend:

- **DECIDED** — we've agreed on the approach; next step is implementation or detailed spec.
- **OPEN** — question raised, not yet resolved.
- **DEFERRED** — acknowledged but intentionally out of scope for the initial build.

---

## 1. Purpose & scope

dev-story is a text-adventure-style interface for a software engineer's daily work, built on hally.  Rooms are states, intents are intents, external tools (workspace-manager, gh, jira, kubectl, git, …) are invoked as host actions.

The app itself is mostly YAML + prompt templates + thin host handlers.  The work in this document is the **platform additions to hally** that dev-story needs.  Everything here is useful beyond dev-story and should be designed as general-purpose primitives.

### 1.1 Summary of additions

| # | Addition | Status |
|---|---|---|
| 1 | Host invocation handler registry | DECIDED (v1) |
| 2 | Proposal pattern (draft → review → execute → result) | DECIDED (v1) |
| 3 | Background jobs + inbox + notifications + teleport | DECIDED (v1) |
| 4 | Room history stack (back navigation) | DECIDED (v1) |
| 5 | Typed workspace context | DECIDED (v1, provisional struct) |
| 6 | Oracle / free-form harness mode | DECIDED (v1) |
| 7 | Multi-modal views (diffs, logs, panels) | DEFERRED (v2) |
| 8 | App-file composition (include / import) | DECIDED (v1) |

Sections 1–4 are the spine.  Everything else layers on top or is room-specific.  Issues surfaced during implementation land in the Deferred subsections and we revisit them.

---

## 2. Host invocation registry

**Purpose:** a general mechanism for YAML effects to call out to external code (CLI tools, APIs) without the YAML author writing Go.

### 2.1 Decisions

- New package `internal/host/` containing a registry keyed by string name, value is a handler function.
- Handlers implement a stable interface:

  ```go
  type Handler func(ctx context.Context, args map[string]any) (Result, error)

  type Result struct {
      Data  map[string]any // bound into world/proposal/job per effect spec
      Error string         // non-empty on expected errors (vs. infra failure)
  }
  ```

- Handlers are registered at process start.  Not hot-reloadable.
- YAML effect shape:

  ```yaml
  invoke: host.name
  with:   { ... }           # args map, templated
  bind:   { key: path }     # optional: extract result.Data into world keys
  ```

- Effects that invoke hosts integrate with the machine's effect executor (today handles `set`/`say`/`emit`) in `internal/machine/`.
- Reference handler for first integration: **workspace-manager**, since it's pre-existing and proves the loop end-to-end.
- **Error handling.**  Handlers return `(Result, error)`.  YAML effect supports an optional `on_error:` transition target that fires when the handler returns a non-nil error.  Error details bind into a reserved `$host_error` slot (`code`, `message`) visible to the error transition's guard.  No per-error-code branching sugar in v1 — authors use expr guards on `$host_error.code`.
- **Auth / secrets.**  Loaded at runtime from env + `~/.hally/secrets.yaml` and injected into handlers via the registry's context struct.  Never referenced from YAML.
- **Allow-list per app.**  Apps declare required hosts in a top-level `hosts: [host.run, host.gh, ...]` manifest section.  Loader errors if YAML invokes an undeclared host.  Runtime errors at startup if a declared host isn't registered.
- **Streaming results** are covered by §4 (jobs).  Synchronous hosts return the simple `Result` above.

### 2.2 Deferred

- **Sandboxing** of host invocations.  v1 trusts registered handlers.  Revisit if we ever load handlers as plugins.
- **Per-error-code branching sugar** in YAML.  v1 uses expr guards on `$host_error.code`.  Revisit if authors find it verbose.

### 2.3 Dependencies

None — this can be built first and independently.

---

## 3. Proposal pattern

**Purpose:** generalize "draft → review/refine → execute → review result" as a reusable DSL construct.  Covers terminal commands, JQL queries, deploys, PR descriptions, code changes, mitigations, docs.

### 3.1 Decisions

- A proposal is a named, typed lifecycle declared at app level and attached to a state.

  ```yaml
  proposals:
    shell_command:
      schema: { cmd: string, cwd: string }
      draft:   { prompt: prompts/shell_draft.md }
      refine:  { prompt: prompts/shell_refine.md }
      execute:
        invoke: host.run
        with: {...}
        repeatable: false
        on_success: { go: stay }     # required when repeatable: false
      views:   { review: ..., running: ..., result: ... }
      policy:  { auto_accept_if: "false", require_confirm: true }

  states:
    terminal:
      proposal: shell_command
  ```

- Loader in `internal/app/` **compiles** `proposal:` into compound sub-states (`drafting`, `reviewing`, `executing`, `reviewing_result`, `done`) with standard transitions pre-wired.  Machine itself stays dumb.
- **`$proposal` field shape** in world state (`internal/world/`):

  ```
  $proposal = {
    id:            string          // ULID, unique within session
    kind:          string          // references the proposal kind in app YAML
    status:        enum            // drafting | reviewing | executing
                                   //   | reviewing_result | done | failed | cancelled
    current:       map<string,any> // latest draft; validated against kind.schema on write
    history: [                     // ordered, oldest first
      { version:     int,          //   1-indexed
        draft:       map<string,any>,  // snapshot of current at that version
        feedback:    string?,      //   user feedback that produced the NEXT version
        produced_at: timestamp }
    ]
    result: {                      // filled on execute completion
      ok:          bool,
      data:        map<string,any>,
      error:       string?,
      started_at:  timestamp,
      finished_at: timestamp?
    }?
    job_ref:       string?         // set while background execute is running
    seeded_from:   string?         // prev proposal id, if created via `new`
    owner_session: string
    created_at:    timestamp
    updated_at:    timestamp
  }
  ```

  Semantics:

  - `status` is written by the runtime on state entry — never by authors.  Source of truth is the machine position; this is a convenience mirror so guards and views don't peek at machine internals.
  - `history[0].feedback` is always null (no prior feedback produced the first draft).
  - `history` is unbounded in the data model; the refine harness decides how much to feed the LLM per call.  Soft cap around 100 entries for memory safety, oldest summarized if exceeded.
  - `result` is a single value; re-runs overwrite it.  Multi-run retention deferred.
  - `job_ref` is cleared when `result` is populated.
  - Kind-level data (schema, prompts, execute, views, policy, repeatable) lives on the kind definition, not the instance — looked up at read time.

- **Template shorthand.**  `$proposal.current.X` is the canonical reference (guards, views, effects).  `{{p.X}}` is sugar for `$proposal.current.X`, available only inside `execute.with` interpolation to keep invocation sites readable.
- **`new` seed behavior.**  A fresh proposal created via `new` starts with `history = []`.  Whether previous `current` values are copied is controlled by the proposal kind's `new.seed: bool` (default false — start empty).  When true, seeded values populate `current` directly and will appear as `version: 1` in history on first refinement.  `seeded_from` always links back to the parent for analytics.
- **Failure and cancel retention.**  Proposals in `failed` or `cancelled` status remain readable in world state (useful for debug and "show me what I cancelled").  Pruned on session end or via explicit archive intent.  Not auto-deleted mid-session.
- **Built-in intents** registered by the compiler (not authors): `refine`, `edit`, `accept` / `run`, `cancel`, `retry`, `rerun`, `modify_and_rerun`, `new`.  Authors add room-specific intents on top.
- **`edit` takes typed slots matching the proposal schema.**  `edit cmd="git status"` mutates `$proposal.current.cmd`.  No free-form diff.
- **Refine harness mode** in `internal/harness/`: a distinct prompt builder that feeds draft history + accumulated user feedback.  Separate from the intent-parsing harness.
- **Nothing external runs until `execute`.**  Review is always a free iteration loop; the approval gate is explicit.
- **Refinement accumulates history.**  All prior drafts + feedback are in context for the next draft so the LLM learns from rejections.
- **Proposals are resumable.**  Persisted via the existing event log; the user can leave the room and come back.
- **Repeatability is opt-in.**  `execute.repeatable: bool` (default false).  When true, `rerun` and `modify_and_rerun` are available on `reviewing_result`.  When false, neither appears and the proposal must terminate on success.
- **Retry is always available after failure.**  Failed executes expose a `retry` intent regardless of `repeatable`.  Retry addresses transient failure, not re-doing a completed thing.
- **Non-repeatable proposals must declare a terminal transition on success.**  `execute.on_success: { go: stay | back | <state> }` is required when `repeatable: false`.  `stay` clears the proposal and re-enters `drafting` in the same room; `back` pops the room history stack (§5); a named state transitions explicitly.
- **"Start fresh" is always available.**  A universal `new` intent on `reviewing_result` closes the current proposal and begins a new one of the same kind, optionally seeded with previous values.  Works regardless of `repeatable`.

Additional v1 decisions folded in:

- **One proposal per state.**  Named slot; multi-proposal deferred.
- **Default `refine` prompt template** shipped at `internal/harness/templates/refine.md` — feeds current draft, accumulated feedback, and a bounded history window.  Authors override per kind via `refine.prompt`.
- **Nested / chained proposals** are authored as two sequential states each with their own proposal plus a transition.  No DSL sugar in v1.
- **Auto-accept policy.**  `auto_accept_if` is an expr evaluated against `{$proposal, $world, $slots}` returning bool.  If true on `drafting → reviewing`, skip straight to `executing`.
- **Shell-command repeatability.**  `host.run` ships with `repeatable: false`.  Dynamic classification deferred.
- **Concurrent ownership.**  Single-session enforced via `owner_session` on the proposal.

### 3.2 Deferred

- **Multi-proposal per state.**  Authors who need it today can model it with compound sub-states.
- **Dry-run phase** as a first-class block.  Model as two chained proposals until the pattern justifies sugar.
- **Slow drafts auto-backgrounding.**  v1 drafts are synchronous — user waits or cancels.  Revisit once jobs are proven and we have real latency data.
- **Dynamic shell-command repeatability.**  Needs classifier prompt + second-run confirmation UX; not worth it pre-usage data.

### 3.3 Dependencies

- §2 (host registry) — `execute.invoke` requires it.
- §4 (jobs) — only for `background: true` executes and slow-draft auto-background.  Proposals work without §4 for synchronous executes.

---

## 4. Background jobs, inbox, notifications, teleport

**Purpose:** coherent concurrency model.  Any long action is a proposal that spawns a job that posts notifications that teleport back to the proposal.

### 4.1 Decisions

- **Jobs are first-class world entities**, persisted in SQLite alongside events:

  ```
  job = { id, kind, status: running|awaiting_input|done|failed|cancelled,
          origin: { state, proposal_id },
          payload, progress, result,
          created_at, updated_at, owner_session }
  ```

- **Triggered declaratively from a proposal's execute:**

  ```yaml
  execute:
    invoke: host.run_tests
    with:   { target: "{{p.target}}" }
    background: true
    on_complete:
      notify: "Tests done: {{job.result.summary}}"
      set:    { last_test_run: "{{job.id}}" }
  ```

- Authors don't wire inbox plumbing; the runtime creates jobs, posts notifications, and maintains teleport links.
- **Notifications** (the inbox's unit):

  ```
  notification = { id, created_at, read,
                   severity: info|success|warn|error|action_required,
                   title, body,
                   teleport: { state, slots, proposal_ref?, job_ref? },
                   origin: "job:abc" | "external:github/pr/123" }
  ```

- **Inbox unifies three streams** (completed jobs, clarification requests, external notifications) under the same notification type.
- **Teleport** is a system transition: bypasses normal guards, rehydrates `$proposal`/`$job`, marks notification read, and logs through the event store for replay determinism.  Teleport pushes the inbox's *predecessor* (not the inbox itself, which is utility) onto the room history stack (§5), so pressing `back` from the teleport destination returns the user to wherever they were before visiting the inbox.
- **Ambient surface** in the TUI:
  - Status-line badge always visible: `inbox: 2 · 1 needs attention`.
  - Interrupt prompt for `action_required` severity before next turn renders.
  - Global intent `go inbox` from every state.
- **Clarification requests** reuse the proposal plumbing.  A stalled job flips to `awaiting_input`, writes a typed clarification schema, posts an `action_required` notification.  Teleport lands in a `clarifying` sub-state of the origin room with the proposal rehydrated and a schema-driven form.
- **Cancellation** must reach the handler.  Only context-aware handlers can be `background: true`; enforced at registration time.

### 4.2 Storage schema

Two new tables in the existing SQLite store.  Introduced via a single migration.

#### jobs

```sql
CREATE TABLE jobs (
  id                   TEXT PRIMARY KEY,        -- ULID
  session_id           TEXT NOT NULL,
  kind                 TEXT NOT NULL,           -- handler name, e.g., "host.run_tests"
  status               TEXT NOT NULL,           -- running|awaiting_input|done|failed|cancelled
  origin_state         TEXT NOT NULL,           -- room where spawned
  origin_proposal_id   TEXT,                    -- nullable; jobs spawned outside a proposal
  payload              TEXT NOT NULL,           -- JSON: the `with` args passed to execute
  progress             TEXT,                    -- JSON: latest snapshot (overwritten)
  result               TEXT,                    -- JSON: on terminal status
  error                TEXT,                    -- string: on failed; reserved value "process_died_mid_job"
  clarification_schema TEXT,                    -- JSON: set while awaiting_input
  clarification_answer TEXT,                    -- JSON: once submitted
  retry_count          INTEGER NOT NULL DEFAULT 0,
  created_at           INTEGER NOT NULL,        -- unix ms (queued)
  updated_at           INTEGER NOT NULL,
  started_at           INTEGER,                 -- actual handler start
  finished_at          INTEGER                  -- terminal timestamp
);

CREATE INDEX jobs_session_status  ON jobs(session_id, status);
CREATE INDEX jobs_session_created ON jobs(session_id, created_at DESC);
```

#### notifications

```sql
CREATE TABLE notifications (
  id                   TEXT PRIMARY KEY,        -- ULID
  session_id           TEXT NOT NULL,
  created_at           INTEGER NOT NULL,
  read_at              INTEGER,                 -- NULL if unread
  dismissed_at         INTEGER,                 -- NULL if active
  snoozed_until        INTEGER,                 -- NULL if not snoozed
  severity             TEXT NOT NULL,           -- info|success|warn|error|action_required
  title                TEXT NOT NULL,
  body                 TEXT,                    -- markdown
  teleport_state       TEXT NOT NULL,
  teleport_slots       TEXT,                    -- JSON
  teleport_proposal_id TEXT,                    -- nullable
  teleport_job_id      TEXT,                    -- nullable
  origin_kind          TEXT NOT NULL,           -- job|external
  origin_ref           TEXT NOT NULL,           -- e.g., "job:abc", "github:pr/123"
  origin_url           TEXT                     -- external deep link if any
);

CREATE INDEX notif_session_unread  ON notifications(session_id, read_at, severity);
CREATE INDEX notif_session_created ON notifications(session_id, created_at DESC);
CREATE INDEX notif_dedup           ON notifications(session_id, origin_kind, origin_ref);
```

#### Semantics

- **Materialized current-state, history in events.**  Tables mirror current status; every progress update, clarification, and retry is also emitted as an event.  Event log is authoritative for replay; the table is fast-read.  `progress` overwrites; `result` is set once per run.
- **Session-scoped; no FKs.**  Everything tagged with `session_id`.  No referential integrity between tables — we never hard-delete rows mid-session, so stale refs aren't a concern.
- **Clarification inline on the job.**  One pending clarification at a time.  If the job returns to `awaiting_input` later, the new schema overwrites.  Clarification history lives in events.
- **Retry updates the existing job row.**  Status back to `running`, `error` cleared, `started_at` updated, `finished_at` cleared, `retry_count++`.  No per-attempt side table.
- **Recovery via reserved error string.**  On startup, supervisor scans for `status='running'` jobs whose `updated_at` is stale, flips them to `failed` with `error='process_died_mid_job'`.  Author `on_failure` policy matches that string.
- **External notifications share the table.**  `origin_kind='external'`, no job row, `origin_url` gives the deep link.  Dedup via the `notif_dedup` index.
- **Snooze hides but preserves unread.**  `snoozed_until > now()` hides from the main inbox list; `read_at` unchanged.
- **Prune = dismiss, not delete.**  `dismissed_at` set on explicit archive or session-end sweep.  Rows stay for replay determinism; physical delete only on session destruction.
- **Badge query hits the unread index.**  `SELECT severity, COUNT(*) FROM notifications WHERE session_id=? AND read_at IS NULL AND dismissed_at IS NULL AND (snoozed_until IS NULL OR snoozed_until < ?) GROUP BY severity` — single index scan, suitable for per-turn refresh.

#### Explicit non-choices

- **No `proposals` table.**  Proposals live in world-state JSON.  Revisit if cross-session querying becomes a need.
- **No per-attempt retry table.**  Retry detail is in events; `retry_count` is the summary column.
- **No FK constraints.**  Schema stays operationally boring.

### 4.3 Additional v1 decisions

- **Job scheduler interface** in new `internal/jobs/` package:

  ```go
  type Scheduler interface {
      Submit(ctx context.Context, spec JobSpec) (JobID, error)
      Cancel(ctx context.Context, id JobID) error
      Subscribe(id JobID) (<-chan JobEvent, func())   // event stream + unsubscribe
      Heartbeat(id JobID, progress any) error         // handler pulse; updates updated_at
  }
  ```

  Goroutine-per-job under a supervisor.  Supervisor scans on startup for stale `running` jobs and marks them failed per §4.2.
- **Heartbeat timeout** defaults to 60s, configurable via `job_heartbeat_timeout`.
- **Progress cadence** is author-driven via `Heartbeat`.  Runtime debounces writes to at most 2/s per job.
- **Fanout defaults.**  Runtime auto-posts a notification on `done`, `failed`, `awaiting_input`.  Proposals opt out with `notify: false` at the proposal level, or per-event with `notify: { done: false, failed: true, awaiting_input: true }`.
- **Clarification collision = error.**  A handler attempting a second `awaiting_input` while one is pending errors out.  Author bug.
- **Archive trigger.**  Session-end sweep dismisses read, info-severity notifications older than 24h and marks terminal jobs for cleanup.  A manual `archive` intent is available from the inbox.
- **No payload size cap in schema**; handler wrapper enforces a 1 MiB default per JSON column and rejects oversize writes.
- **Interrupt UX.**  `action_required` posts a single-line banner (`[enter] open · [esc] later`) above the input line without blocking entry.
- **Cancellation.**  `cancel_job {id}` is a global intent.  When a job is cancelled, its originating proposal returns to `reviewing` with the proposal's `current` preserved — user can refine and retry or `new`.

### 4.4 Deferred

- **External notification ingestion** (Jira/GitHub/Slack).  Implemented as a host handler posting to the same notifications table via a recurring background job.  App-level concern, not platform.
- **Multi-run result retention** on repeatable proposals.  v1 overwrites.

### 4.5 Dependencies

- §2 (host registry) — jobs wrap host handlers.
- §3 (proposals) — proposals are the primary job producer.

---

## 5. Room history stack

**Purpose:** a back-navigation primitive shared by three features: proposal `on_success: back` (§3), inbox teleport return (§4), and Oracle Room exit (§7).  One mechanism, three consumers.

### 5.1 Decisions

- **Stack in world state.**  A bounded list of prior state references (with slots bound on arrival) maintained by the runtime.
- **Push on transition-in.**  Entering a state pushes the previous state onto the stack, unless the transition is marked stackless.
- **Utility transitions are stackless.**  Transitions flagged `push_history: false` don't record.  Default opt-outs: entering the Inbox Room and the Oracle Room — they're side-trips, not substantive progress.
- **Universal `back` intent.**  Available from every room.  Pops the top of the stack and transitions there.
- **Empty-stack fallback.**  `back` with an empty stack lands on Main Room rather than erroring.
- **Bounded depth.**  Cap at 10 entries.  Pushes beyond the cap evict the oldest.
- **Reset points.**  Transitioning to the Main Room clears the stack — Main is the root.  Other states may opt in to reset via a clear-history effect.
- **Teleport interaction.**  Inbox teleport pushes the inbox's *predecessor* (not the inbox itself) onto the stack.  `back` from the teleport destination returns the user to wherever they were before visiting the inbox.
- **Persistence.**  Stored as part of world state — survives session resume.

Additional v1 decisions folded in:

- **Per-transition flag** (`push_history: false`).  No per-state category sugar in v1.
- **Slot rehydration on pop.**  Each stack entry records the slots bound on arrival; `back` restores them when popping.
- **Back within compound states** is horizontal — popping returns to the previous unrelated room, not up the state hierarchy.  A separate `exit` intent can be introduced later if hierarchy-pop is needed.
- **No programmatic push from effects.**  Stack manipulation stays tied to transitions for determinism.

### 5.2 Dependencies

None.  Can be built independently.  Unblocks §3 (`on_success: back`), §4 (teleport return), and §7 (Oracle return).

---

## 6. Typed workspace context

**Purpose:** the Workspace Room and its children (Bug Fix, Implementation, Debug, Test, …) need rich structured context (repos, branches, PRs, issues, dirty state) that today's flat world key/value can't express cleanly.

### 6.1 Decisions

- **Provisional `$workspace` struct** in `internal/world/` with well-known fields: `id`, `root_path`, `repos: [{path, branch, dirty}]`, `issue_id?`, `pr_ids: []`.  Labeled provisional — revisited if a schema DSL proves needed.
- **Workspace identity via ambient binding.**  Entering the Workspace Room sets `$workspace`.  Subsequent sub-rooms inherit it.  Leaving the workspace hierarchy clears the binding.
- **workspace-manager integration.**  Host handler `host.workspace_manager.get` invokes the CLI and parses JSON output.  Called on entry to the Workspace Room and refreshed on explicit user action.
- **Multi-repo is transparent.**  The `repos` array handles both cases; single-repo workspaces have `len(repos) == 1`.  No DSL distinction.

### 6.2 Deferred

- **Schema DSL for typed world values.**  Revisit if the provisional struct grows unwieldy.
- **Automatic refresh on filesystem change.**  v1: explicit refresh only.

### 6.3 Dependencies

- §2 (host registry) for `host.workspace_manager.get`.

---

## 7. Oracle / free-form harness mode

**Purpose:** the Oracle Room is open-ended Q&A with read-only access.  It doesn't advance the state machine — the user can ask anything and come back.

### 7.1 Decisions

- **New `ConversationalHarness`** in `internal/harness/`: tool-use enabled, no intent parsing, no machine transitions.  Returns Markdown to display in the Oracle Room's view.
- **Read-only tool allow-list** enforced at the harness (not overridable by app YAML): file read, code search (grep), doc search, web fetch.  Explicitly excluded: shell exec, file write, external writes, host invocations that mutate state.
- **Return path** via §5 — Oracle entry is `push_history: false`, so `back` from Oracle returns to the caller.
- **Flat state for v1** — Oracle has no internal sub-states.  Re-enter on each question if future needs require it.

### 7.2 Dependencies

- §5 (room history) for return navigation.

---

## 8. Multi-modal views

**Purpose:** dev-story needs diffs, log lines, structured result tables — not just prose.

### 8.1 Open questions

- **Markdown extensions.**  Glamour supports fenced code blocks with syntax highlighting.  Is that sufficient for diffs and logs?  Probably enough for v1.
- **Panel concept.**  Room description + live data panel side-by-side (e.g., a running job's log tail next to the inbox item view).  Currently the TUI is one transcript.  Needs a layout model.  **DEFERRED** to v2; workaround is dedicated sub-states for live views.
- **Images / attachments.**  Jira screenshots, Confluence embeds.  **DEFERRED**.

---

## 9. App-file composition

**Purpose:** dev-story has ~18+ rooms.  One monolithic YAML gets unmaintainable.

### 9.1 Decisions

- **Include directive.**  `include: [rooms/*.yaml, proposals/*.yaml]` at the top of the main app file.  Glob supported.
- **No namespacing or auto-prefixing.**  State, proposal, and host keys are app-global.  Collisions across included files are loader errors — authors rename explicitly.
- **Merge semantics.**  `states`, `proposals`, and `hosts` maps from all included files are merged at parse time into a single app definition.  Order-independent.

### 9.2 Dependencies

None — can be built independently as soon as dev-story gets big enough to hurt.

---

## 10. Implementation roadmap

Order is chosen so each step unlocks the next and produces something testable.

1. **Host registry (§2)** + workspace-manager handler.  Proves end-to-end host invocation.  Small.
2. **App-file composition (§9)** before dev-story gets big.  Small and pays for itself immediately.
3. **Room history stack (§5)**.  Small, standalone, unblocks Oracle return and proposal `on_success: back`.
4. **Proposal pattern (§3)**, synchronous executes only.  Build Terminal Room as the reference case.  Tests the compilation, refine harness, `repeatable`, `on_success`, and world-state typed entries.
5. **Job scheduler (§4)** + `background: true` + basic inbox state with manual navigation.  No ambient surface yet.  Build Test Room and Deploy Room (draft only) against this.
6. **Notifications + teleport (§4)**.  Ambient status-line badge.  Now the inbox is actually useful from anywhere.
7. **Clarification requests (§4)**.  Closes the loop on long-running ops that need input.
8. **Typed workspace context (§6)** — provisional struct approach.  Unlocks Workspace Room and its children.
9. **Oracle harness mode (§7)**.  Ships last; orthogonal to everything else.
10. **Multi-modal view polish (§8)** as needed.

Each step should land with: DSL example, at least one dev-story room using it, and a test fixture in hally's existing test runner.

---

## 11. Cross-cutting concerns

### 11.1 Determinism & replay

Everything we add must flow through the event log so `hally replay` keeps working.  Specifically: job progress events, notification posts/reads, teleports, clarifications, proposal drafts/refines, history pushes/pops.  Host invocations are the one non-deterministic edge — the event log records inputs and outputs so replay substitutes the recorded output.

### 11.2 Security

Design §11 defines the sandbox model.  Additions here:

- Host registry: per-app allow-list (see §2.2).
- Proposals: the approval gate is the primary human-in-the-loop guard; `auto_accept_if` must be expr-validated, not free-form code.  `repeatable: false` is the default — opt-in is required for anything re-runnable after success.
- Jobs: cancellable handlers only for `background: true`.

### 11.3 Observability

Operators will want: active jobs, failed jobs in last hour, notification backlog, proposal abandonment rate.  Export via the existing event store — no new surface needed for v1.

### 11.4 Testing

- Proposals: Mode 2 flow fixtures should be able to inline a full draft/refine/execute/result cycle with the LLM mocked.
- Jobs: fixtures should support "job completes with result X after N virtual ticks" without real async.
- Teleport: assert that rehydrated state matches pre-teleport state.
- Room history: assert that `back` after N transitions lands where expected, including across teleports.

Needs extensions to `internal/testrunner/` — specifically, a virtual clock and a way to stub job completion.

---

## 12. Glossary

- **Room** — a state in the hally state machine, as seen by the user.
- **Intent** — a named user action, declared in YAML, parsed from free text by the LLM.
- **Proposal** — a draft-review-execute-result lifecycle attached to a state.  Standard intents come free.
- **Repeatable** — a proposal property indicating whether its execute effect is safe to perform more than once after success.  Default false.
- **Job** — a persistent record of a long-running host invocation.  Produced by `background: true` executes.
- **Notification** — an inbox entry.  May originate from a job, a clarification, or an external system.
- **Teleport** — a system-initiated transition that rehydrates context.  Used to jump from inbox items back to their origin.
- **Clarification** — a special job state where the job is paused awaiting user input, surfaced via the inbox.
- **Room history** — a bounded stack of prior rooms, used by the `back` intent for pop-style navigation.
- **Host** — external code (CLI, API) invoked from YAML via the registry.
