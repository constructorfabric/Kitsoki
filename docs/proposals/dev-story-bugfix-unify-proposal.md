## Proposal — devstory as overseer: unify bugfix + cypilot + implementation, dogfood on kitsoki itself

**Status:** Draft v2. Nothing implemented yet. Hard-depends on shipped
imports/composition (`docs/stories/imports.md`) and on the pending
[bug-format proposal](bug-format-proposal.md) for the on-disk ticket
schema. v1 of this doc was bugfix-only; v2 folds in cypilot's SDLC
waterfall as a third sub-story and lifts the dogfood loop to be the
primary objective.

**Primary objective (PoC).** Close the loop on kitsoki itself: a user
hits a bug in a running kitsoki app, types `/meta kitsoki bug`, the
agent files `issues/bugs/<id>.md` against this repo (per
`bug-format-proposal.md`), and *that same file* feeds the bugfix
pipeline — interactively or autonomously — when the user later runs
`stories/kitsoki-dev/app.yaml`. The fix lands as a real commit/PR
against kitsoki; the bug file's `status:` flips to `resolved`. **No
external tracker, no Jira, no Slack — just kitsoki working on kitsoki
through its own UI.** Same story shape works on a kitsoki story
(`stories/cloak/`) and, with a provider rebind, on cyber-repo via
Jira/Bitbucket.

**Goal.** Move dev-story, bugfix, **cypilot** (planning/SDLC waterfall),
and implementation into `stories/` as first-class, general-purpose
kitsoki sub-stories that work against **any** ticket-tracker / VCS /
CI provider. Ship four concrete providers: **local files** (bug/task
as Markdown in `issues/`), **GitHub** (issues + code + PRs + Actions),
**Jira/Bitbucket** (lives in cyber-repo), and **cypilot artifacts**
(PRD/ADR/DESIGN/FEATURE files at well-known paths). One bugfix state
machine, one cypilot waterfall, four transports, zero code changes
when switching providers.

**Mechanism.** The provider abstraction is `host_interfaces:` (already
shipped — see `docs/stories/imports.md` §11 and `stories/robbery/`). The
general-purpose stories declare named capability surfaces (`ticket`,
`vcs`, `ci`, `workspace`, `transport`, plus cypilot's `artifact`)
with typed `operations:` blocks. Each provider is a small kitsoki
package (Go host handlers, optionally wrapped in a tiny "binding-only"
app) that registers handlers. A top-level app picks providers by
binding them through `imports.<alias>.host_bindings:` — no edits to
the imported story.

**Architectural tenet — one set of YAMLs, two execution modes.** A
bugfix (and a cypilot run, and an implementation) must work
**supervised** (a human watches the TUI and types intents at
checkpoints) or **autonomous** (an LLM-judge or external poller drives
the same checkpoints) from **the same `app.yaml`** — no parallel
"interactive" / "headless" variants, no `if-autonomous` conditionals
in state bodies. The only thing that varies is *who answers the
checkpoint*. This is the most important property in the proposal,
because both modes will be common — autonomous for known-shape work
(routine bug, well-scoped feature), supervised for everything else,
with the modes **mixable mid-run** (an LLM-judge that bails out to a
human at a hard checkpoint is the default, not an edge case).

**Judge polymorphism.** Each checkpoint state declares a `judge:`
mode: `human` (user types the reply intent in TUI), `llm` (an
LLM-judge prompt runs at the checkpoint and emits a schema-validated
reply intent), or `llm_then_human` (try LLM, fall through to human if
the LLM's confidence is low, its validator rejects N times, or the
schema returns `verdict: uncertain`). The judge is just one more
host call inside the checkpoint's `on_enter`; flipping modes is a
world-key change (`world.judge_mode`), not a state-graph change. See
§4.4.

**Inbox + transport are co-equal channels, not competing modes.** The
local TUI inbox and the external transport (Jira/GitHub comments,
Slack) are two views of the same notification stream — never a
toggle. A Jira comment arriving while a user is in the TUI appears as
an inbox item that mirrors the comment; a user typing `accept` in
TUI causes a `[Bot]` comment to be posted to Jira so the autonomous
loop sees it. Neither channel "owns" the reply — first-write-wins,
acknowledged on the other channel. See §4.5.

**Unification: three sub-stories under one hub.** Real engineering work
splits roughly into three flows that share the same provider
interfaces and the same world shape:

| Sub-story | Trigger | Shape |
|---|---|---|
| `bugfix` | Something broke. A ticket with `type: bug` exists. | Reproduce → propose → implement → test → review → validate → PR. Today: cyber-repo's 14-phase pipeline. |
| `cypilot` | Something new, large. Requires SDLC artifacts before code. | PRD → ADR + DESIGN → DECOMPOSITION → FEATURE → CODE, each with a validate gate. Today: cyber-repo's `cypilot/` tooling, agent-driven via `/cypilot-plan`, `/cypilot-generate`, `/cypilot-analyze`. **Story authored in kitsoki initially for fast iteration, migrates to the cypilot upstream repo once stable** (§5.5). |
| `implementation` | Something new, small enough to skip full SDLC. A ticket with `type: task` exists. | Review task → write code → test → review → PR. |

All three import `stories/pr-refinement/` as a first-class sub-story
for the shared tail (open PR → CI monitor → resolve comments →
re-push → merge). `dev-story` is the engineer's-day hub that imports
all three plus the day-level rooms (main, inbox, oracle,
workspace_manager, ticket_search, standup, code_review,
observability, deploy, incident, docs).

**Cyber-repo's two existing pipelines collapse to imports.** Today
cyber-repo has both the 14-phase autonomous bugfix and the
`cpt analyze|plan|generate` workflows — two complete systems with two
control surfaces. Under this proposal: the 14 phases become
`@kitsoki/bugfix`'s autonomous mode; the cypilot workflows become
`@kitsoki/cypilot`'s autonomous mode; both are driven by the same
loop.py via the same `kitsoki session continue` interface.

**TL;DR.**

- `stories/bugfix/` — reusable bug-fix pipeline (formerly cyber-repo
  `stories/bugfix/`).
- `stories/cypilot/` — reusable SDLC waterfall (PRD→ADR→…→CODE).
- `stories/implementation/` — reusable small-task pipeline.
- `stories/_lib/review_and_pr/` — shared tail; imported by all three.
- `stories/dev-story/` — engineer's-day hub; imports bugfix + cypilot
  + implementation + debug + planning + refactor.
- `stories/kitsoki-dev/` — **the dogfood instance**: binds local-files
  provider; reads `issues/bugs/*.md` and `issues/features/*.md` in
  this repo; works on kitsoki itself AND on any story under
  `stories/<name>/issues/`. This is the PoC.
- `cyber-repo/stories/devstory/` — thin wrapper: `imports:
  '@kitsoki/dev-story'` with `host_bindings:` to Jira/Bitbucket/
  Jenkins/standctl, plus cyber-only extension rooms (stands, mirror,
  panopticum, component_registry).
- Providers shipped in kitsoki: `internal/hosts/localfiles/`,
  `internal/hosts/github/`, `internal/hosts/cypilot_artifacts/`,
  plus existing oracle/transport. Jira/Bitbucket/Jenkins live in
  cyber-repo.
- **Closed loop**: `/meta kitsoki bug` writes `issues/bugs/<id>.md`
  → ticket_search picks it up → bugfix pipeline → real PR → file
  status flips to resolved. Both `meta` flows (kitsoki-self and
  per-story) feed the *same* pipeline; only the target-root differs.

---

## 1. Current state

Five things exist today, none in the right place:

| Where | What | Problem |
|---|---|---|
| `testdata/apps/dev-story/` | 20-room stub, Jira-named world keys (`jira_query`, `jira_results`), all hosts return canned echoes via `host.run` | Not in `stories/`. Jira is hard-wired into world shape. Bugfix room is a tiny dead-end stub. |
| cyber-repo `.worktrees/devstory/stories/devstory/` | Fork of the stub, rewired against Jira/Bitbucket/standctl/Jenkins. 20 rooms, all hosts real. | Hard fork — every change in the kitsoki base must be copy-pasted. README explicitly says "we copied it and rewired every room". |
| cyber-repo `.worktrees/devstory/stories/bugfix/` | Standalone 14-phase autonomous pipeline driven by `tools/loopy/loop.py` via `hally session continue`. v1-shippable. | Separate app from devstory — the interactive bugfix room and the autonomous pipeline are two implementations of the same logical flow. Hard-coded to Jira/Bitbucket. |
| cyber-repo `cypilot/` | Artifact-first SDLC toolkit (`cpt` CLI + agent skills). Workflows: `analyze`, `plan`, `generate`. Artifact kinds: PRD, ADR, DESIGN, DECOMPOSITION, FEATURE, CODE. | Lives entirely as agent skills + markdown workflows; **no kitsoki story exists for it**. Drives Claude through TOML phase files in `.plans/<task>/phase-NN-*.md` but has no interactive UI or persistent session model. Same pipeline shape as bugfix's checkpoint flow, modelled completely differently. |
| `cmd/kitsoki/bug.go` + `internal/agents/bug_reporter.md` | `/meta bug` files story bugs under `<app-dir>/bugs/`. | Today only the story target works; kitsoki-self target is the subject of [`bug-format-proposal.md`](bug-format-proposal.md). The on-disk path is `bugs/` today; the bug-format proposal moves it to `issues/bugs/`. |

Five ideas in `ideas.md` already call for the fix:

> extensible stories - reusable dev story w/ company and project-
> specific aspects (rooms, intents, etc... as reusable building
> blocks - extended and composed)

> ticket/pr/etc... providers that support bitbucket jira github or
> file mode for testing/dev match our existing bugfix artifact write
> pattern

> these providers are behind mcp for use in sessions, pluggable
> backends with the same interface so a single prompt works across
> different implementations

> file story bug or kitsoki bug with a similar interface/pattern, if
> kitsoki is local write to file (we are in dev mode and stories +
> kitsoki source are local)

> LLM-driven local review checklist, docs, testing, etc... add it to
> devstory to make it real

Imports landed (commit `7331630`). The bug-format proposal lands the
canonical `issues/bugs/<id>.md` schema with `target: story|kitsoki`
frontmatter and richer fields (`severity`, `component`, `trace_ref`,
`external`, etc.) — this proposal consumes that schema and is
otherwise the cash-out for the other four ideas.

## 2. Provider abstraction — five `host_interfaces:`

The general-purpose stories declare named capability surfaces. Each
operation has a typed input/output schema. Providers register
handlers; importers bind providers via `host_bindings:`.

### 2.1 `ticket` — issue tracker

```yaml
host_interfaces:
  ticket:
    description: "Issue tracker abstraction (file / GitHub Issues / Jira)."
    operations:
      search:
        input:  { query: string, limit: int }
        output: { tickets: list }      # [{id, title, status, priority, assignee, url}, …]
      get:
        input:  { id: string }
        output: { id: string, title: string, body: string, status: string,
                  priority: string, assignee: string, url: string, comments: list }
      comment:
        input:  { id: string, body: string, thread: string }
        output: { ok: bool, comment_id: string }
      transition:
        input:  { id: string, to: string }
        output: { ok: bool }
      list_mine:                       # "what's on my plate"
        input:  { filter: string }
        output: { tickets: list }
```

### 2.2 `vcs` — version control + PR/MR host

```yaml
host_interfaces:
  vcs:
    description: "Branch / commit / PR abstraction (git / GitHub / Bitbucket)."
    operations:
      branch:
        input:  { workdir: string, name: string, base: string }
        output: { ok: bool, branch: string }
      diff:
        input:  { workdir: string }
        output: { diff: string, files: list }
      commit:
        input:  { workdir: string, message: string, files: list }
        output: { ok: bool, sha: string }
      push:
        input:  { workdir: string, remote: string }
        output: { ok: bool, url: string }
      open_pr:
        input:  { workdir: string, title: string, body: string, base: string }
        output: { ok: bool, url: string, pr_id: string }
      pr_status:
        input:  { pr_id: string }
        output: { state: string, checks: list, comments: list }
      pr_comment:
        input:  { pr_id: string, body: string }
        output: { ok: bool }
```

### 2.3 `ci` — build & test runner

```yaml
host_interfaces:
  ci:
    description: "Build/test runner (local make/go test, GitHub Actions, Jenkins)."
    operations:
      run_tests:
        input:  { workdir: string, target: string }
        output: { ok: bool, passed: int, failed: int, log: string, junit: string }
      build:
        input:  { workdir: string, target: string }
        output: { ok: bool, log: string }
      remote_status:                  # post-push CI checks
        input:  { pr_id: string }
        output: { state: string, checks: list }
```

### 2.4 `workspace` — per-task working tree

```yaml
host_interfaces:
  workspace:
    description: "Working-copy manager. Local: `git worktree`. Cyber: workspace-manager CLI."
    operations:
      list:
        input:  {}
        output: { workspaces: list }
      get:
        input:  { id: string }
        output: { id: string, path: string, branch: string, dirty: bool }
      create:
        input:  { name: string, ticket_id: string, base: string }
        output: { ok: bool, path: string }
      sync:
        input:  { id: string }
        output: { ok: bool, log: string }
```

### 2.5 `transport` — where checkpoint artifacts are delivered

```yaml
host_interfaces:
  transport:
    description: "Out-of-band channel for posting proposals, checkpoints, status."
    operations:
      post:
        input:  { thread: string, body: string }
        output: { ok: bool, message_id: string }
```

### 2.6 `artifact` — cypilot's PRD/ADR/DESIGN/FEATURE store

Cypilot adds one more interface. SDLC artifacts behave differently
from tickets: each kind has a template and a validator; each artifact
*depends on* a prior artifact being validated; multiple artifacts of
the same kind can coexist (a project has many PRDs).

```yaml
host_interfaces:
  artifact:
    description: "Cypilot SDLC artifact store (PRD / ADR / DESIGN / DECOMPOSITION / FEATURE / CODE)."
    operations:
      list:
        input:  { kind: string }                     # "prd" | "adr" | "design" | "feature" | …
        output: { artifacts: list }                  # [{id, kind, title, path, status, …}]
      get:
        input:  { id: string }
        output: { id: string, kind: string, title: string, body: string,
                  frontmatter: object, path: string, depends_on: list }
      create:
        input:  { kind: string, title: string, slug: string, parent_id: string }
        output: { ok: bool, id: string, path: string }
      validate:                                      # the cypilot-analyze workflow
        input:  { id: string, mode: string }         # "deterministic" | "semantic" | "consistency"
        output: { ok: bool, findings: list, report: string }
      decompose:                                     # the cypilot-plan workflow
        input:  { id: string }                       # a PRD or DECOMPOSITION id
        output: { ok: bool, plan_path: string, phase_count: int }
```

These map 1:1 onto cypilot's three workflows (`cypilot-analyze` →
`validate`, `cypilot-plan` → `decompose`, `cypilot-generate` →
`create` + body write). The provider implementation (§6.4) shells
out to the existing `cpt` CLI for v1; a native Go reimplementation
can come later.

### 2.7 Built-ins reused

`oracle` is already a kitsoki built-in (`host.oracle.ask_with_mcp`);
the stories use it directly — no new interface needed. Same for the
proposal lifecycle (`docs/embedded/apply-proposal.md`) — bugfix and
cypilot both use the existing proposal/review/execute machinery for
destructive actions.

## 3. Story layout

```
kitsoki/
├── stories/
│   ├── bugfix/                          # general-purpose bug-fix pipeline
│   │   ├── app.yaml                     # imports pr-refinement for the tail
│   │   ├── README.md                    # contract: world_in/out, exits, intents, host_interfaces
│   │   ├── rooms/
│   │   │   ├── idle.yaml
│   │   │   ├── reproducing.yaml         # phase 1 + 1.5
│   │   │   ├── proposing.yaml           # phase 3
│   │   │   ├── implementing.yaml        # phase 6 + 6.5
│   │   │   ├── testing.yaml             # phase 7 + 7.5
│   │   │   ├── reviewing.yaml           # phase 8 + 9 (code + security)
│   │   │   └── validating.yaml          # phase 9.5–9.7
│   │   ├── prompts/                     # artifact-producing AND judge prompts
│   │   ├── schemas/                     # JSON-Schema for submit + judge verdicts
│   │   └── flows/                       # both supervised + autonomous + mixed fixtures
│   │
│   ├── cypilot/                         # SDLC waterfall (PRD→ADR→DESIGN→DECOMPOSITION→FEATURE→CODE)
│   │   │                                # INTERIM HOME — migrates to cypilot upstream repo (§5.5, Phase 8)
│   │   ├── app.yaml                     # imports pr-refinement for the tail
│   │   ├── README.md
│   │   ├── rooms/
│   │   │   ├── idle.yaml
│   │   │   ├── prd.yaml                 # draft → analyze → validated
│   │   │   ├── adr.yaml                 # parallel with design
│   │   │   ├── design.yaml
│   │   │   ├── decomposition.yaml       # produces .plans/<slug>/phase-NN-*.md
│   │   │   ├── feature.yaml             # one room, walked N times for N features
│   │   │   └── code.yaml                # per-feature implement; calls iface.artifact.* and host.run
│   │   ├── prompts/
│   │   ├── schemas/
│   │   └── flows/
│   │
│   ├── implementation/                  # small-task pipeline
│   │   ├── app.yaml                     # imports pr-refinement for the tail
│   │   ├── rooms/                       # review_task, write_code, test, review
│   │   └── ...
│   │
│   ├── pr-refinement/                   # FIRST-CLASS — not a library
│   │   ├── app.yaml                     # standalone runnable: kitsoki run … --pr-id N
│   │   ├── README.md                    # contract for upstream stories that import it
│   │   ├── rooms/
│   │   │   ├── open_pr.yaml             # creates the PR via iface.vcs.open_pr
│   │   │   ├── ci_monitoring.yaml       # polls iface.vcs.pr_status
│   │   │   ├── diagnose.yaml            # CI failure analysis (LLM-judge often, human-bail)
│   │   │   ├── resolve_comments.yaml    # round-trip review comments
│   │   │   ├── re_push.yaml
│   │   │   └── merge.yaml
│   │   ├── prompts/
│   │   ├── schemas/
│   │   └── flows/
│   │
│   ├── code-review/                     # reviewing teammates' PRs
│   │   └── ...                          # triggered from inbox; reusable
│   │
│   ├── dev-story/                       # engineer's-day hub — imports all the above
│   │   ├── app.yaml
│   │   ├── README.md
│   │   ├── rooms/                       # main, inbox, oracle, workspace_manager,
│   │   │                                #  ticket_search (renamed from jira_search),
│   │   │                                #  standup, deploy, observability, incident, docs
│   │   ├── prompts/
│   │   └── flows/
│   │
│   ├── kitsoki-dev/                     # ★ the dogfood instance ★
│   │   ├── app.yaml                     # imports dev-story with local-files bindings
│   │   ├── README.md                    # the PoC walkthrough (§5.4)
│   │   └── scenarios/                   # warp bases for demo'ing flows
│   │
│   └── (existing imports demo: robbery, frontier_event, oregon-trail)
│
├── internal/
│   ├── hosts/
│   │   ├── localfiles/                  # ticket via issues/bugs/*.md (per bug-format), vcs=git, ci=local
│   │   ├── github/                      # ticket via gh issues, vcs via gh, ci via gh run
│   │   ├── cypilot_artifacts/           # iface.artifact.* — shells out to cpt CLI (v1)
│   │   ├── workspace/                   # git-worktree-backed workspace manager
│   │   ├── inbox/                       # host.inbox.add — mirrors transport posts into TUI inbox
│   │   └── transport/                   # stdout / file-append / github-comment / jira-comment
│   ├── judges/                          # LLM-judge harness: schema-validated verdict + confidence
│   └── agents/                          # story-bug-reporter, kitsoki-bug-reporter, judge agents
│
├── cmd/kitsoki/                         # registers local-files + github providers by default
└── issues/                              # NEW — dogfood ticket store (per bug-format-proposal)
    ├── README.md
    ├── bugs/
    │   ├── 2026-05-14T103205Z-tui-hangs-on-esc.md
    │   └── 2026-05-14T112011Z-foyer-banner-truncated.md
    └── features/                        # PRD-track tickets (cypilot-handled)
        └── 2026-05-14T120000Z-multi-app-mode.md

cyber-repo/
└── stories/
    └── devstory/                        # cyber flavor — ~30 lines of YAML
        ├── app.yaml                     # imports '@kitsoki/dev-story', binds jira/bb/jenkins
        ├── README.md
        ├── rooms/                       # cyber-only rooms (NOT a fork of the base!)
        │   ├── stands.yaml
        │   ├── mirror.yaml
        │   ├── panopticum.yaml
        │   └── component_registry.yaml
        ├── prompts/                     # cyber-specific overrides for LLM phases
        ├── hosts/                       # Go: jira, bitbucket, jenkins, standctl, workspace-manager
        └── overrides/                   # per docs/stories/imports.md §10 — surgical replacements only
```

The cyber-repo `stories/bugfix/` standalone disappears: its 14 phases
become the imported bugfix from kitsoki, and loop.py drives it the
same way (`kitsoki session continue jira:<TICKET>`). The cyber-repo's
`tools/loopy/pr-refine.py` is also subsumed — its work is what
`stories/pr-refinement/` does, just driven from kitsoki rather than
a separate Python script. And cyber-repo's `cypilot/` agent skills
get re-cast as the autonomous mode of `stories/cypilot/` (the
artifacts on disk and the `cpt` CLI both stay; the orchestration moves
into a kitsoki state machine).

## 4. The unification

### 4.1 One state machine, two transports

The 14-phase pipeline and the interactive bugfix room are two views of
the same machine. The proposal collapses them.

**Visible rooms** (what an interactive user sees):

```
idle → reproducing → proposing → implementing → testing → reviewing → validating → done
```

**Phases inside each room** (what loop.py advances autonomously):

| Room | Phases | Checkpoint at room exit |
|---|---|---|
| reproducing | phase_1 (bug reproduction), phase_1_5 (service trace), phase_1_7 (coverage review) | yes — `reproduction_artifact` |
| proposing | phase_3 (fix proposal), phase_4 (missing tests), phase_5 (missing specs) | yes — `propose_fix_artifact` |
| implementing | phase_6 (implementation plan), phase_6_5 (apply fix) | (no checkpoint, threads into testing) |
| testing | phase_7 (test review), phase_7_5 (test implementation) | yes — `implement_review_artifact` |
| reviewing | phase_8 (code review), phase_9 (security review) | (no checkpoint, threads into validating) |
| validating | phase_9_5 (build & deploy), phase_9_6 (version check), phase_9_7 (validation script) | yes — `validate_artifact` |
| done | phase_12 (open PR), phase_12_5 (CI monitor), phase_13 (process review / knowledge) | yes — `done_artifact` |

Each room has:
- An `_executing` substate that auto-runs the phase's `on_enter` chain (`host.run` → fetch context → `host.oracle.ask_with_mcp` → bind artifact). Same as today.
- An `_awaiting_reply` substate that pauses at the checkpoint and posts the artifact via `host.transport.post` (the iface call — TUI for interactive, Jira comment for autonomous).
- The same reply intents in both modes: `accept` / `refine` / `restart_from` / `jump_to` / `quit`. The user types them; loop.py maps Jira comment text to them via the existing `checkpoint_intents` library.

This is already how kitsoki sessions work — the room is just one
state-machine; what changes between modes is **the transport binding**
and **the source of intent input**. Both are already pluggable.

### 4.2 Interactive shortcuts

An interactive user does not want to walk 14 phases to apply a one-line
patch. The general-purpose `bugfix` story adds three intent shortcuts
inside each `_awaiting_reply`:

| Intent | Effect |
|---|---|
| `skip_to_pr` | Jump to `validating` with `restart_from_stage="validate"` — skips phases 4, 5, 7 (test reports / planning) when the change is trivial. |
| `quick_fix` | One-shot path: reproducing → proposing → implementing → testing → done. Collapses to ~5 LLM calls. |
| `full_pipeline` | Default — walk all phases. What loop.py uses. |

`world.bugfix_mode: { type: string, enum: [quick, full], default: full }`
gates which paths are reachable. Autonomous callers explicitly set
`bugfix_mode=full`; the user can pick at `idle`.

### 4.3 Bugs, tasks, features — and the shared tail is a story too

The current cyber-repo bugfix is bug-specific (reproduction, repro
stands, root cause). The dev-story stub has both `bugfix` and
`implementation` rooms. Cypilot is the SDLC-waterfall flavor. And the
**PR refinement tail** — open PR, watch CI, address review comments,
re-push, merge — is a flow of its own that cyber-repo already runs as
a separate worktree (`tools/loopy/pr-refine.py`,
`.worktrees/pr-refinement/`).

The proposal keeps every reusable pipeline as a **first-class story**,
not a library import:

| Story | Triggered by | Shape | Imports |
|---|---|---|---|
| `bugfix` | Ticket `type: bug` | reproduce → propose → implement → test → review → validate → handoff | `pr-refinement` (for the tail) |
| `cypilot` | Ticket `type: feature_prd` or unscoped large work | PRD → ADR + DESIGN → DECOMPOSITION → FEATURE → CODE → handoff | `pr-refinement` (for the tail) |
| `implementation` | Ticket `type: task` | review_task → write_code → test → review → handoff | `pr-refinement` (for the tail) |
| `pr-refinement` | Open PR exists (or just produced by upstream) | open_pr → ci_monitoring → diagnose → resolve_comments → re-push → merge | (no further sub-imports) |
| `code-review` | Inbox notification: "PR awaiting your review" | list → review_pr → comment → approve_or_request_changes | (no further sub-imports) |

`stories/pr-refinement/` is **its own story** — not a library, not an
include, not a `_lib/` directory. It has its own `app.yaml`, its own
rooms, its own flows, runs standalone (`kitsoki run
stories/pr-refinement/app.yaml --pr-id 142` works for arbitrary PR
hygiene), and is imported by bugfix/cypilot/implementation via the
same `imports:` block any other sub-story uses. This is the pattern:
**if a flow is reusable across sub-stories, it gets promoted to its
own story directory — not buried.**

The handoff from bugfix/cypilot/implementation into pr-refinement is
just an import edge:

```yaml
# stories/bugfix/app.yaml
imports:
  pr:
    source: ../pr-refinement
    entry: open_pr
    world_in:
      ticket_id:      "{{ world.ticket_id }}"
      workdir:        "{{ world.workdir }}"
      feature_branch: "{{ world.feature_branch }}"
      pr_title:       "{{ world.proposed_pr_title }}"
      pr_body:        "{{ world.proposed_pr_body }}"
    exits:
      merged:    { to: done,     set: { pr_url: "{{ world.pr__url }}", status: "merged" } }
      abandoned: { to: archived, set: {} }
```

### 4.4 Judgement — human, LLM, or LLM-then-human, all in the same YAML

The defining property of this design: **the same checkpoint state
works in supervised, autonomous, or hybrid mode without any
conditional branching in the state body.** A `judge:` mode parameter
on the `_awaiting_reply` state selects who answers.

```yaml
bugfix_reproduce_awaiting_reply:
  description: "Reproduction artifact posted; awaiting verdict."
  on_enter:
    # Always: post the artifact to whichever transport is bound.
    - invoke: iface.transport.post
      with:
        thread: "{{ world.thread }}"
        body:   "{{ world.reproduction_artifact.summary_markdown }}"
    # And always: mirror the artifact into the inbox so the local TUI
    # sees the pending checkpoint regardless of who will answer.
    - invoke: host.inbox.add
      with:
        kind:    checkpoint
        title:   "Reproduction artifact: {{ world.ticket_id }}"
        thread:  "{{ world.thread }}"
        state:   bugfix_reproduce_awaiting_reply
    # Conditionally: ask an LLM-judge if the world says to.
    - when: "world.judge_mode == 'llm' || world.judge_mode == 'llm_then_human'"
      invoke: host.oracle.ask_with_mcp
      with:
        prompt:  prompts/judge_reproduction.md
        schema:  schemas/judge_verdict.json   # emits {verdict, intent, reason, confidence}
        context: "{{ world.reproduction_artifact }}"
      bind:
        llm_verdict: "submitted"
    # And conditionally: auto-fire the LLM's verdict if confident.
    - when: |
        world.judge_mode != 'human' &&
        world.llm_verdict.confidence >= 0.8 &&
        world.llm_verdict.intent != 'uncertain'
      effects:
        - emit_intent: "{{ world.llm_verdict.intent }}"
          slots: { feedback: "{{ world.llm_verdict.reason }}" }
  on:
    # The reply intents are identical regardless of who fired them.
    accept:         [{ target: bugfix_propose_executing }]
    refine:         [{ target: bugfix_reproduce_executing, effects: [{ set: { refine_feedback: "{{ slots.feedback }}" }}, { set: { cycle: "{{ world.cycle + 1 }}" }}]}]
    restart_from:   [{ target: bugfix_idle }]
    quit:           [{ target: "@exit:abandoned" }]
```

The three modes that fall out of this:

| `world.judge_mode` | Behaviour at every `_awaiting_reply` |
|---|---|
| `human` | Post + inbox-mirror; wait for an explicit reply intent. (No LLM call.) |
| `llm` | Post + inbox-mirror + run LLM-judge; if confident, auto-fire the intent. If not confident *and* `judge_mode == 'llm'`, the validator/schema constraints rejects N times → the state holds and waits forever. Operator must change `judge_mode` to `human` to unstick. (This is the rigid mode — useful for batch overnight runs that should never escalate.) |
| `llm_then_human` (recommended default for autonomous runs) | Post + inbox-mirror + run LLM-judge; auto-fire if confident; **fall through to human** otherwise — the state just sits at the checkpoint with the inbox item visible. A passing human picks it up. **No clean-mode failure** — uncertainty is always recoverable. |

**Failure bail-out from LLM to human is the default mode.** The
proposal does not advocate `judge_mode: llm` (the strict mode); it
exists as a knob for batch jobs where a human will sweep the queue
later. The recommended autonomous default is `llm_then_human`:
machines do the easy work, humans do the hard.

The `judge:` polymorphism is one host call per checkpoint, gated by
`when:`. It is **not** a fork in the state graph. The same YAML works
unchanged across all three modes — flip `world.judge_mode` at session
start and the behaviour changes.

### 4.5 Inbox + transport — co-equal channels with first-write-wins

A bugfix can run while a user is logged into the TUI **and** a Jira
comment thread is open on the same ticket. The proposal makes these
the two halves of one bus:

| Direction | What happens |
|---|---|
| **Artifact out** (`on_enter` of a checkpoint) | `iface.transport.post` fires once → message lands in Jira/GitHub/Slack. `host.inbox.add` fires once → mirror item appears in the local TUI inbox. The mirror item links back to the transport's message ID for traceability. |
| **Reply in via TUI** | User types `accept` (or any reply intent) at the checkpoint state. The state's `on:` arc fires. Before transitioning, an `on_exit` effect calls `iface.transport.post` with a `[Bot]` prefix so external watchers see the decision. |
| **Reply in via comment** | Loop.py / webhook polls the transport, sees a new comment from an authorised author, maps it to a reply intent via `checkpoint_intents`, calls `kitsoki session continue`. The state's `on:` arc fires — identical to the TUI path. Before transitioning, `host.inbox.add` posts a "reply received from <author> via <channel>" notification to the local inbox so a TUI user who happens to be looking sees what was decided. |
| **Conflict** (TUI user replies *and* a comment arrives within the same checkpoint window) | First-write-wins. The losing reply is folded into the inbox as an info notification (`"<author> attempted <intent> after checkpoint resolved"`). The state has already advanced; there is no rollback. |

Two consequences worth flagging:

1. **The inbox is never optional.** Even in pure-autonomous mode with
   no TUI session attached, the inbox writes still happen — they go
   into the session journal (per `continue-mode-proposal.md`) and
   become visible the moment any TUI attaches via `--continue`.
2. **The transport is never optional either, even in pure-supervised
   mode.** A user working alone on a local bug still posts to the
   transport — for kitsoki-dev that transport is `host.stdout` (or
   `host.append_to_file` writing to `issues/bugs/<id>.md` itself).
   The principle: every reply produces an artifact trail, regardless
   of who fired it.

These two properties are what stops the TUI and the comment thread
from being "two parallel control planes." There is one control plane;
the channels are I/O surfaces. **Both always run.**

## 5. Top-level app composition

### 5.1 `stories/dev-story/app.yaml` (general-purpose)

```yaml
app:
  id: dev-story
  version: 0.1.0
  title: "dev-story: Engineer's Day"

# No provider bindings here — concrete bindings happen at the
# instance level (kitsoki-dev, cyber-repo/devstory). This app
# declares the surface and routes between sub-stories.

host_interfaces:
  ticket:    { default: host.local_files.ticket }
  vcs:       { default: host.git }
  ci:        { default: host.local }
  workspace: { default: host.local_files.workspace }
  transport: { default: host.stdout }

imports:
  bf:
    source: ../bugfix
    entry: idle
    intents: { import: [reproduce_bug, apply_fix, verify_fix, open_pr] }
    exits:
      done:      { to: pr_room, set: { pr_url: "{{ world.bf__pr_url }}" } }
      abandoned: { to: main,    set: {} }

  impl:
    source: ../implementation
    entry: idle
    intents: { import: [review_task, write_code, open_pr] }
    exits:
      done:      { to: pr_room }
      abandoned: { to: main }

  # … debug, planning, refactor follow the same shape

root: main

include:
  - rooms/*.yaml          # main, inbox, oracle, workspace_manager, ticket_search,
                          # standup, code_review, pr_room, deploy, observability,
                          # incident, docs (cyber-specific rooms NOT here)
```

The `bf` and `impl` sub-stories are reachable as parent states. The
parent's `main.yaml` has an intent `go_bugfix` that targets `bf` (which
enters at `bf.idle`, fires `world_in:`, then routes to the child's
`idle` state).

### 5.2 `stories/kitsoki-dev/app.yaml` (the dogfood instance)

```yaml
app:
  id: kitsoki-dev
  version: 0.1.0
  title: "kitsoki-dev — work on kitsoki itself (and its stories) with local files"

imports:
  core:
    source: ../dev-story
    entry: main
    hosts: declared
    host_bindings:
      ticket:    host.local_files.ticket      # reads issues/bugs/*.md and issues/features/*.md
      vcs:       host.git                     # local git CLI
      ci:        host.local                   # `go test ./...` / per-target test cmd from world
      workspace: host.git_worktree            # .worktrees/<id>
      artifact:  host.cypilot_artifacts       # cypilot's PRD/ADR/etc. via cpt CLI
      transport: host.append_to_file          # **NOT stdout** — appends a comment block to the bug file
                                              # so the local file is the comment thread too
    world_in:
      repo_root:    "{{ env.PWD }}"
      ticket_globs:
        - "issues/bugs/*.md"                  # kitsoki self-bugs
        - "issues/features/*.md"              # kitsoki PRD-track features
        - "stories/*/issues/bugs/*.md"        # per-story bugs (from `/meta story bug`)
        - "stories/*/issues/features/*.md"    # per-story features
      judge_mode:   "human"                   # supervised by default; user can flip per session
      autonomous_default_mode: "llm_then_human"

root: core
```

That is the entire dogfood app — about 25 lines. The whole engineer's
day becomes usable against the local kitsoki repo (AND all of its
stories) immediately. `transport: host.append_to_file` means
checkpoint posts get appended **into the bug file itself** as
`## Comment <timestamp> by <kitsoki|llm-judge|user>` blocks — the
file is both the ticket and the conversation thread; nothing is lost
when the session ends.

### 5.3 `cyber-repo/stories/devstory/app.yaml`

```yaml
app:
  id: cyber-devstory
  version: 0.1.0
  title: "devstory — cyber-repo flavor"

imports:
  core:
    source: '@kitsoki/dev-story'
    entry: main
    hosts: declared
    host_bindings:
      ticket:    host.jira
      vcs:       host.bitbucket
      ci:        host.jenkins
      workspace: host.workspace_manager     # the cyber-repo CLI
      transport: host.jira_comment
    world_in:
      repo_root:  "{{ env.PWD }}"
      proxy_url:  "https://127.0.0.1:3128/bitbucket"

# Cyber-only extension rooms — added to the parent's flat state tree
# via include (they sit at the top level alongside core's main, inbox, …).
# They reference `core__` aliased state keys when crossing into the
# imported flow.
include:
  - rooms/stands.yaml             # spin/check/clean stands via standctl
  - rooms/mirror.yaml             # mirror branch / build
  - rooms/panopticum.yaml         # component metadata queries
  - rooms/component_registry.yaml # cross-check Jira component vs registry

root: core
```

Loop.py changes one line — `kitsoki session create --app
cyber-repo/stories/devstory/app.yaml --external-key jira:<TICKET>` —
and dispatches `set_session_context` against the imported
`core__bf__set_session_context` intent (re-exported by the import
block in `dev-story/app.yaml`).

### 5.4 The closed-loop dogfood walkthrough (the PoC)

This is what the proposal exists to enable. Every step is real; no
external services involved.

```
 ┌─────────────────────────────────────────────────────────────────────┐
 │  Session 1 — user is running a kitsoki story (e.g. cloak)           │
 │  $ kitsoki run stories/cloak/app.yaml                               │
 ├─────────────────────────────────────────────────────────────────────┤
 │  User hits a bug in the TUI (Esc hangs in main.foyer).              │
 │  User types: /meta kitsoki bug                                       │
 │                                                                      │
 │   ↳ Meta-mode opens with the kitsoki-bug-reporter agent.            │
 │   ↳ Agent reads the trace file, asks for expected vs actual.        │
 │   ↳ Agent calls: kitsoki bug create --target kitsoki                │
 │                    --title "Esc in foyer hangs the TUI"             │
 │                    --component tui --trace-ref traces/…             │
 │   ↳ File written: $KITSOKI_REPO/issues/bugs/                        │
 │                     2026-05-14T103205Z-tui-hangs-on-esc.md           │
 │   ↳ Frontmatter: target=kitsoki, status=open, kitsoki_rev=7331630   │
 └─────────────────────────────────────────────────────────────────────┘
                              │
                              │ (later, same day or next morning)
                              ▼
 ┌─────────────────────────────────────────────────────────────────────┐
 │  Session 2 — user opens the dogfood instance                        │
 │  $ kitsoki run stories/kitsoki-dev/app.yaml                         │
 ├─────────────────────────────────────────────────────────────────────┤
 │  Lands in dev-story's `main` room.                                   │
 │  User: ticket search → "open kitsoki bugs"                          │
 │   ↳ iface.ticket.list_mine matches the glob                          │
 │     $KITSOKI_REPO/issues/bugs/*.md where target=kitsoki, status=open │
 │   ↳ Returns 3 bugs including the one filed in session 1.             │
 │                                                                      │
 │  User picks the Esc-hangs bug → enters bugfix sub-story.            │
 │  User stays in supervised mode (world.judge_mode = "human").        │
 │                                                                      │
 │  bugfix walks:                                                       │
 │    reproducing  → LLM produces a repro plan; user `accept`s.        │
 │    proposing    → LLM proposes a fix; user `refine`s with feedback. │
 │                  → LLM re-proposes; user `accept`s.                 │
 │    implementing → LLM emits a patch; iface.vcs.commit writes it.    │
 │    testing      → iface.ci.run_tests runs `go test ./...`           │
 │    reviewing    → LLM-judge approves; user reviews and `accept`s.   │
 │    validating   → final smoke test passes.                          │
 │    handoff      → enters pr-refinement sub-story:                    │
 │                    open_pr → iface.vcs.open_pr (git push +           │
 │                    optional gh pr create); ci_monitoring polls       │
 │                    until checks green; merge fires.                  │
 │                                                                      │
 │  At every checkpoint, `host.append_to_file` writes a                 │
 │  `## Comment 2026-05-14T14:22:01Z by user` block into                │
 │  issues/bugs/2026-05-14T103205Z-tui-hangs-on-esc.md so the bug       │
 │  file IS the conversation log.                                       │
 │                                                                      │
 │  On merge, an effect sets the bug's frontmatter:                     │
 │    status: resolved                                                  │
 │    resolved_at: 2026-05-14T15:00:00Z                                 │
 │    resolved_in_commit: <sha>                                         │
 └─────────────────────────────────────────────────────────────────────┘
                              │
                              │ Loop closed.
                              ▼
                The bug that the user hit in cloak is fixed in
                kitsoki by kitsoki, through its own UI, with the
                file that the meta-mode wrote serving as ticket,
                comment thread, and audit trail.
```

**The autonomous variant of the same loop** (set
`world.judge_mode = llm_then_human` at session start, run with
`--detach`): bugfix walks all phases without user input, falling
through to human only at hard checkpoints (security review, final
merge). The user comes back to a TUI with the inbox showing
"reviewing_awaiting_reply: artifact ready" and either approves or
refines.

**The story-bug variant of the same loop**: same flow, but the bug
file is at `stories/cloak/issues/bugs/<id>.md` (filed by `/meta
story bug`). The dogfood's `ticket_globs:` matches it through the
`stories/*/issues/bugs/*.md` pattern. Same pipeline, same providers,
same loop — only the file path is different. **This is what
"devstory oversees kitsoki AND its stories" means in practice.**

**The cyber-repo equivalent of the same loop**: identical sequence,
except `/meta kitsoki bug` is replaced by a Jira comment, the
`ticket` iface is bound to `host.jira` instead of
`host.local_files.ticket`, and the merge target is Bitbucket. The
state graph is unchanged because it doesn't care.

### 5.5 `stories/cypilot/` lives in kitsoki today, cypilot repo tomorrow

The cypilot story is the one general-purpose sub-story whose **final
home is not kitsoki**. The cypilot upstream — the repo that ships the
`cpt` CLI, the `.core/` template/rules/checklist machinery, and the
`~/.cypilot/cache/` content — is the right author-of-record for the
cypilot SDLC state machine, the same way cyber-repo is the
author-of-record for `stories/devstory/` once Phase 7 lands.

**For v1 we author it in kitsoki anyway**, for two reasons:

1. The artifact-pipeline shape (which checkpoints to gate, what each
   judge prompt looks like, how `cpt analyze` failures route) is
   genuinely unknown. Iterating it inside the kitsoki tree — next to
   `stories/bugfix/`, sharing the same flow runner, getting validated
   by the same `kitsoki test flows` — is much faster than
   round-tripping through a separate repo on every change.
2. The dogfood instance needs cypilot working *now* to handle
   features in `issues/features/*.md`. Waiting on a cross-repo
   migration would block the PoC.

**Migration to cypilot repo is mechanical** once the story stabilises
(see Phase 8, §8):

| Step | What moves | What stays |
|---|---|---|
| 1 | `kitsoki/stories/cypilot/{app.yaml, rooms/, prompts/, schemas/, flows/, README.md}` → `cypilot/stories/cypilot/` (or whichever path the cypilot repo prefers) | `kitsoki/internal/hosts/cypilot_artifacts/` (the Go provider) — stays in kitsoki, no reason to move it. |
| 2 | `stories/dev-story/app.yaml` import line: `source: ../cypilot` → `source: '@cypilot/cypilot'` | Everything else in dev-story is unchanged. The `@cypilot/<name>` resolver walks up from the importer just like `@kitsoki/<name>` (docs/stories/imports.md §"Source resolution"); add `.cypilot-root` marker or a `cypilot` module name to the upstream repo's root. |
| 3 | Cyber-repo `stories/devstory/` already imports `@kitsoki/dev-story` — its cypilot reach-through goes through dev-story's import, so it picks up the new path automatically. | No cyber-repo change needed. |
| 4 | kitsoki's `stories/cypilot/` directory is deleted; the migration is one-way (no copy left behind, no symlink hack). | Git history preserves the authoring trail. |

The pattern is identical to what cyber-repo's `stories/devstory/`
does in Phase 7: a downstream repo's `app.yaml` reaches up to a
canonical upstream via the `@<repo>/<story>` resolver. **There is
nothing special about cypilot here** — it's the same composition
mechanism, just with three repos in the chain (cypilot publishes →
kitsoki dev-story imports → cyber-repo devstory binds providers).

A consequence: the **cypilot upstream repo must adopt kitsoki as a
build/test dependency** once Phase 8 lands (it needs `kitsoki test
flows` to validate its own story). That's a real coupling and worth
flagging — if cypilot's release cadence is much slower than
kitsoki's, the story can fall behind imports-loader changes. Mitigation:
keep `imports:` semantics strictly backwards-compatible per the
shipped contract (the `imports.md` v1 surface). This proposal commits
to that.

## 6. Provider implementations

### 6.1 `local-files` (ships in kitsoki)

The ticket store is a directory of Markdown files with YAML
frontmatter conforming to the schema in
[`bug-format-proposal.md`](bug-format-proposal.md) §2. The proposal
inherits that schema verbatim — `target`, `kitsoki_rev`,
`component`, `severity`, `trace_ref`, `external`, etc. The dogfood
adds two operational extensions that the bug-format proposal leaves
for §1.1 "list / show (deferred to v1.1)":

- `list_mine` scans **all** the configured `ticket_globs:` (kitsoki
  self-bugs + per-story bugs + features), filters by `target` and
  `assignee`, and merges the result. The query string is matched
  against `title`, `labels`, and `component` (kitsoki only) /
  `state_path` (story only).
- `comment` appends a `## Comment <ISO-date> by <author>` block at the
  end of the file body. The author is `kitsoki` for transport posts,
  `llm-judge` for LLM-fired replies, `<git user>` for TUI replies.
  This is what makes the bug file double as the conversation thread.

Mapping to `ticket.operations`:

| Op | Implementation |
|---|---|
| `search` | grep over `ticket_globs:`, frontmatter parse, fuzzy title match |
| `get` | read file, parse frontmatter + body |
| `comment` | append `## Comment <ISO-date> by <author>` block to the file body |
| `transition` | rewrite `status:` in frontmatter; commit if `repo_root` is a git repo |
| `list_mine` | merge across all `ticket_globs:`, filter by target + assignee |

VCS provider: shell out to `git`. CI provider: shell out to the
configured target — `go test ./...` for kitsoki, `kitsoki test flows
stories/<n>/app.yaml` for stories, per-`world.test_cmd` override.
Workspace: `git worktree add .worktrees/<id> <base>`.

### 6.2 `github` (ships in kitsoki)

Uses `gh` CLI (assume installed, like the existing
`tools/standctl` pattern). Ticket = GitHub Issues, vcs = `gh pr ...`,
ci = `gh run ...`. Transport = `gh issue comment` /
`gh pr comment`. One Go package, ~400 LOC. Sync with local files via
the `external:` frontmatter block from
[`bug-format-proposal.md`](bug-format-proposal.md) §4 — a bug filed
locally can be later mirrored to a GitHub issue without losing the
local-file source of truth.

### 6.3 `jira-bitbucket` (stays in cyber-repo)

Already exists in cyber-repo as `tools/loopy/`'s direct REST calls
(`bug-fix.py`, `pr-refine.py`). The proposal repackages those as Go
host handlers under `cyber-repo/stories/devstory/hosts/`. The
auth/proxy/ZTA pattern documented in
`cyber-repo/.worktrees/devstory/stories/devstory/README.md` carries
over verbatim. Approx. one Go file per surface (jira.go,
bitbucket.go, jenkins.go).

### 6.4 `cypilot_artifacts` (ships in kitsoki permanently, shells to `cpt`)

The **provider** (Go host handlers for `iface.artifact.*`) lives in
kitsoki for the long term — it's a thin shell-out layer with no
cypilot-specific intelligence. The **story** (`stories/cypilot/` —
rooms, prompts, schemas, flows) is **authored in kitsoki initially**
to keep the feedback loop tight while we iterate the artifact-pipeline
shape, then **migrates to the cypilot upstream repo** (`cypilot/`)
once stable. See §5.5 for the cross-repo handoff.

The provider implementation does NOT reimplement cypilot's
template/checklist/rules engine in Go — that machinery lives in
`~/.cypilot/cache/`, gets vendored into each repo at `cypilot/.core/`,
and is well-tested as-is. The kitsoki host is a thin Go wrapper
that:

| Op | Implementation |
|---|---|
| `list` | `cpt artifact list --kind <k>` (today's cypilot CLI; may need a `--json` flag added) |
| `get` | read the artifact file directly (path conventions per `cypilot/config/artifacts.toml`) |
| `create` | `cpt generate --kind <k> --title <t> --slug <s> --parent <p>` (the existing `cypilot-generate` workflow, now headlessly invoked) |
| `validate` | `cpt analyze --target <id> --mode <m>` |
| `decompose` | `cpt plan --task <id>` (writes `.plans/<slug>/phase-NN-*.md`) |

The cypilot story's autonomous mode walks PRD → ADR/DESIGN → … by
firing these ops in sequence; each artifact-producing room is a
proposal-gated checkpoint, exactly like a bugfix phase. The
**judge** at each artifact's checkpoint is typically
`llm_then_human` (LLM-judge runs the artifact through its `analyze`
mode; if the report is clean, auto-accept; otherwise wait for human
input). This is how cypilot's strict validation gates fit into the
common control-plane.

In v1 the cypilot story is **autonomous-only** for the artifact
phases (drafting a PRD interactively in a TUI is a lousy UX); the
human supervision still happens at the validate checkpoints. v2 can
add interactive drafting rooms once a clean prose-editing surface is
on the table.

## 7. World shape — the lingua franca

Every general-purpose state in `bugfix` references world keys with
provider-neutral names. The provider's `bind:` block in `on_enter`
populates them from whatever shape the underlying API returns.

```yaml
world:
  # Identity
  ticket_id:        { type: string, default: "" }      # "KS-1" / "PLTFRM-99999" / "kitsoki#42"
  ticket_title:     { type: string, default: "" }
  ticket_status:    { type: string, default: "" }
  ticket_url:       { type: string, default: "" }
  thread:           { type: string, default: "" }      # transport-specific thread id
  allowed_authors:  { type: list,   default: [] }      # ACL for autonomous reply

  # Workspace
  workspace_id:     { type: string, default: "" }
  workdir:          { type: string, default: "" }
  base_branch:      { type: string, default: "" }
  feature_branch:   { type: string, default: "" }

  # Pipeline state
  bugfix_mode:      { type: string, default: "full" }  # full | quick
  cycle:            { type: int,    default: 0 }       # L2 retry counter
  last_reply_author: { type: string, default: "" }
  refine_feedback:   { type: string, default: "" }
  jump_to:           { type: string, default: "" }
  restart_from_stage: { type: string, default: "" }

  # Per-phase artifacts (room-grain, not phase-grain — collapsed)
  reproduction_artifact:    { type: object, default: {} }
  propose_fix_artifact:     { type: object, default: {} }
  implement_review_artifact: { type: object, default: {} }
  validate_artifact:        { type: object, default: {} }
  done_artifact:            { type: object, default: {} }

  # PR
  pr_id:            { type: string, default: "" }
  pr_url:           { type: string, default: "" }
  ci_state:         { type: string, default: "" }
```

This world shape is binding-neutral — no `jira_`, no `bitbucket_`, no
`bugs_`. Provider handlers map from their wire format into these keys.

## 8. Migration plan

Eight phases. Each can land independently. **The dogfood loop closes
at Phase 3**, which is the PoC milestone. Existing cyber-repo bugfix
keeps running unchanged through Phase 7. Phase 8 (cypilot story moves
to its upstream repo) is optional from kitsoki's standpoint — happens
on the cypilot team's schedule, not ours.

### Phase 0 — Land `bug-format-proposal.md` (prerequisite, kitsoki)

This proposal hard-depends on the canonical
`issues/bugs/<id>.md` layout, the `group + verb` meta-mode triggers
(`/meta story bug` / `/meta kitsoki bug`), and the
`story-bug-reporter` + `kitsoki-bug-reporter` agent split. All three
are documented in [`bug-format-proposal.md`](bug-format-proposal.md)
Phases A + B and must ship before Phase 2 here.

### Phase 1 — General-purpose `stories/bugfix/` skeleton + judge polymorphism (kitsoki)

Build `stories/bugfix/` with the visible-rooms model from §4.1 and
provider-neutral world from §7. Implement the **judge polymorphism**
(§4.4) from day one — `world.judge_mode` is `human` / `llm` /
`llm_then_human` from the first commit. Implement only the **happy
path** (no cycle budgets, no checkpoint intents beyond
`accept` / `refine` / `quit`). Add the six `host_interfaces:`
declarations. Local-files provider sufficient — flows pass with stub
data.

Acceptance: `kitsoki test flows stories/bugfix/app.yaml` passes ~12
flows covering all three judge modes on the happy path.

### Phase 2 — `stories/pr-refinement/` and `stories/dev-story/` (kitsoki)

`pr-refinement` lands as a first-class story (§4.3). Move the
relevant rooms from `testdata/apps/dev-story/` into
`stories/dev-story/`, strip Jira-specific world keys, rewrite hosts
as iface calls. Add `bf` + `pr` imports; the bugfix's `handoff` exit
maps to `pr-refinement`'s entry. Wire `host.local_files.*` and
`host.inbox.add` Go handlers (the inbox+transport co-existence from
§4.5).

Acceptance: A bug filed via `/meta kitsoki bug` is reachable from
`stories/dev-story/` via ticket search → bugfix → pr-refinement →
done, all in supervised mode.

### Phase 3 — `stories/kitsoki-dev/` (the dogfood PoC) ★

Build the dogfood instance per §5.2. Create `issues/bugs/README.md`
in this repo. File one or two real kitsoki bugs as the smoke-test
seed (the TUI-hangs-on-Esc one is a known live issue — perfect first
ticket). Run the closed loop from §5.4 end to end. Both supervised
and `llm_then_human` autonomous variants should work.

**Acceptance — the PoC succeeds when**: a kitsoki bug filed by
`/meta kitsoki bug` in one session is fixed via the dogfood instance
in a second session, the diff lands as a real commit, the file's
`status:` is `resolved`. **Same loop with a story-bug** (filed via
`/meta story bug` against `stories/cloak/`) works on the same
`kitsoki-dev` app via the multi-glob `ticket_globs:`. This is the
deliverable.

### Phase 4 — Cycle budgets, refine, full checkpoint intents (kitsoki)

Port the L2 cycle-budget pattern, `refine` / `restart_from` /
`jump_to` from the existing cyber-repo bugfix into the kitsoki
general-purpose `stories/bugfix/`. Re-run the cyber-repo's flow
fixtures against the kitsoki story to confirm parity. The flow files
move to `stories/bugfix/flows/` and become provider-neutral.

Acceptance: All 34 flow scenarios from cyber-repo bugfix pass against
the kitsoki story. Cycle-budget retry arcs work in all three judge
modes.

### Phase 5 — `stories/cypilot/` (in kitsoki, interim home) + GitHub provider (kitsoki)

Build `stories/cypilot/` with the artifact-pipeline rooms per §3.
**The story is authored in kitsoki for fast iteration**; its final
home is the cypilot upstream repo (§5.5, Phase 8). Land
`internal/hosts/cypilot_artifacts/` shelling out to `cpt`. Implement
`internal/hosts/github/` in parallel — they exercise different `iface`
surfaces and don't conflict.

Acceptance: A PRD-track ticket walked through cypilot produces a
validated PRD + DESIGN + DECOMPOSITION + N feature files via the
`cpt` CLI, all gated by LLM-judge analyze runs with human bail-out
on uncertain verdicts. GitHub provider: open a real kitsoki GitHub
issue, work it through bugfix, get a PR opened.

### Phase 6 — `stories/implementation/` and `stories/code-review/` (kitsoki)

The two remaining sub-stories that fill out dev-story's hub. Both
reuse pr-refinement for the tail; code-review is triggered from the
inbox when an external "PR awaiting review" notification arrives.

Acceptance: A teammate's PR (mocked, or a real GitHub PR) can be
reviewed via `stories/code-review/` end to end.

### Phase 7 — Cyber-repo migrates to imports (cyber-repo)

Replace cyber-repo `.worktrees/devstory/stories/devstory/` (the fork)
and `.worktrees/devstory/stories/bugfix/` (the standalone pipeline)
and the agent-only `cypilot/` orchestration with a single thin
`cyber-repo/stories/devstory/app.yaml` per §5.3. Cyber-only rooms
(stands, mirror, panopticum, component_registry) move in alongside
it. loop.py switches its `--app` argument and the `HALLY_DISPATCH`
path collapses (the dispatch happens through normal kitsoki session
continue, no special branch).

Acceptance: `tools/loopy/test_e2e_jira.py` passes against the new
kitsoki-backed app. The 20-file devstory fork disappears. The
standalone `stories/bugfix/` worktree merges its real work into
`@kitsoki/bugfix` and the worktree retires.

### Phase 8 — `stories/cypilot/` migrates to the cypilot repo (cypilot)

Once Phase 5's cypilot story is stable (subjective: it has run real
PRD-track features end to end without churn for some agreed period),
move `kitsoki/stories/cypilot/` into the cypilot upstream repo per
§5.5. The cypilot provider (`internal/hosts/cypilot_artifacts/`)
stays in kitsoki. The `@cypilot/<name>` resolver gets a
`.cypilot-root` marker (or the cypilot repo's `go.mod` already
declares a `cypilot` module name — either works per `imports.md`'s
source-resolution rules).

`stories/dev-story/app.yaml` flips one import line:

```yaml
imports:
  cyp:
-   source: ../cypilot
+   source: '@cypilot/cypilot'
    entry: idle
    # … unchanged
```

Acceptance: `stories/kitsoki-dev/` and `cyber-repo/stories/devstory/`
both still walk a cypilot pipeline end to end after the source path
flip. The cypilot upstream's CI runs `kitsoki test flows
stories/cypilot/app.yaml` against the migrated story. No regressions
in cypilot's existing `cpt` CLI tests.

This phase is optional from kitsoki's standpoint — Phase 5's
arrangement (cypilot story hosted in kitsoki) is fully working code.
Phase 8 happens when the cypilot team is ready to take ownership,
not on kitsoki's clock.

## 9. What this proposal does NOT change

- **The shipped imports machinery.** Everything here uses
  `imports:`, `host_interfaces:`, `host_bindings:`, `exits:`,
  `world_in/set:` as documented in `docs/stories/imports.md`. No loader
  changes.
- **The existing oracle/transport host registry.** Both stay.
- **loop.py's autonomous-driver model.** Same `set_session_context` +
  `continue` shape, same `checkpoint_intents` mapping. Different
  `--app` path.
- **The 14-phase artifact schemas.** They survive; the proposal
  collapses them into 5 *rooms* but the underlying phase template,
  on-enter chain, and validator MCP attachment all stay.

## 10. Open questions

1. **Provider granularity — one binding per iface, or one per op?**
   `host_bindings: { ticket: host.github }` is convenient; the runtime's
   prefix-fallback (`host.github.search` falls back to `host.github`)
   already supports it. But for `vcs`, a user may want `vcs.branch` via
   local git and `vcs.open_pr` via GitHub. The op-level binding is
   already allowed by the loader (`vcs__open_pr: host.github`), so this
   is a documentation/ergonomics call. Recommended: document the
   op-level form for mixed-provider setups; default to iface-level.

2. **LLM-judge schema — separate judge prompts, or reuse the
   artifact's schema with a "verdict" field?** §4.4 sketches a
   distinct `judge_verdict.json` schema (`{verdict, intent, reason,
   confidence}`). The alternative is to extend each artifact's own
   schema with an optional `self_verdict:` block and have the same
   LLM call produce both. Recommended: separate prompts and schemas.
   Mixing artifact-production with judgement biases the model toward
   approving its own work; a fresh-context judge prompt is more
   honest. Costs one extra LLM call per checkpoint — acceptable.

3. **Confidence threshold for `llm_then_human` auto-fire — what's
   the default?** §4.4 shows `0.8`. Too low and the LLM steamrolls
   weak verdicts through; too high and the autonomous mode rarely
   actually advances on its own. Recommended: make it per-room
   (`world.judge_confidence_threshold` defaultable per state) and
   start at `0.8`. Tune from cyber-repo bugfix's existing acceptance
   data once Phase 7 lands.

4. **Inbox-mirror granularity — every transport post, or only
   checkpoint posts?** §4.5 says "every reply produces an artifact
   trail" but doesn't distinguish "transport posts" (artifact
   announcements) from "transport replies" (judgement acks). v1
   recommendation: mirror BOTH, but tag them differently
   (`kind: checkpoint` vs `kind: ack`); the TUI inbox view can filter
   acks out by default to reduce noise.

5. **Cypilot's "interactive drafting" — v1 or v2?** Phase 5 ships
   cypilot in autonomous-only mode for the artifact-production
   phases. A user typing PRD prose into a TUI is a poor UX. v2 can
   add an interactive drafting room once we have a prose-editing
   surface (which is its own design question). Recommended: defer.

6. **PR refinement scope — does it own the merge, or just the
   refinement loop?** Strict reading: `stories/pr-refinement/`
   handles open_pr → CI → comments → re-push, and the parent
   sub-story (bugfix/cypilot/implementation) makes the final merge
   decision via a separate intent. Pragmatic reading: pr-refinement
   handles everything including merge, since by then all the
   judgement has happened. Recommended: pragmatic — pr-refinement
   owns the merge, parent story just consumes the `merged` exit.

7. **Naming — `dev-story` vs `devstory`?** The kitsoki repo uses
   `dev-story` (hyphenated); cyber-repo uses `devstory` (unhyphenated).
   Recommended: standardise on `dev-story` (kitsoki current convention)
   for the general-purpose app; let cyber-repo keep `devstory` as the
   instance name since it's the user-visible binding the existing
   tooling expects.

8. **`issues/features/` vs `issues/tickets/` vs unified `issues/`?**
   The bug-format proposal opens `issues/` as a namespace with `bugs/`
   as the v1 subdirectory; this proposal adds `features/` for cypilot
   intake. An alternative is one flat `issues/` directory with the
   `type:` frontmatter field doing all the routing. Recommended:
   subdirectories — they let `ls issues/bugs/` work as a quick
   "what's broken?" check without parsing every file.

## 11. Out of scope (deferred)

- **MCP servers wrapping the providers.** `ideas.md` calls for
  providers exposed via MCP for cross-app reuse. This proposal makes
  the providers usable through kitsoki's host registry first;
  wrapping them as MCP servers is mechanical once the interfaces
  stabilise.
- **Story registry / versioning.** `imports:` accepts a `version:`
  field today but doesn't enforce it. A real registry (`@kitsoki/<n>`
  resolving to a versioned tarball rather than a relative path)
  belongs in a separate proposal.
- **Cross-repo flows.** Working on a cyber-repo bug from a kitsoki-
  hosted devstory by importing the cyber-repo's host bindings into a
  kitsoki app would be powerful but needs a story for credential
  scope. Defer.

## 12. See also

- [`bug-format-proposal.md`](bug-format-proposal.md) — **hard
  prerequisite**: the `issues/bugs/<id>.md` schema, the `group + verb`
  meta-mode triggers, and the kitsoki-vs-story target split. Phase 0
  of this proposal's migration is "land bug-format Phases A + B."
- [`docs/stories/imports.md`](../stories/imports.md) — the composition primitives this
  proposal stands on.
- [`docs/architecture/hosts.md`](../architecture/hosts.md) — host registry + iface dispatch
  semantics, including the prefix-fallback used by `iface.X.op`
  dispatch.
- [`continue-mode-proposal.md`](continue-mode-proposal.md) — the
  session journal that makes the inbox+transport mirror survive
  TUI restart (§4.5 depends on this).
- `stories/robbery/`, `stories/frontier_event/`, `stories/oregon-trail/`
  — three-layer composition demo this proposal mirrors.
- `cyber-repo/.worktrees/devstory/stories/bugfix/README.md` — current
  14-phase bugfix pipeline; the autonomous-control surface this
  proposal preserves and generalises.
- `cyber-repo/.worktrees/devstory/stories/devstory/README.md` — current
  20-file fork; what this proposal collapses into a ~30-line import.
- `cyber-repo/cypilot/.core/workflows/{plan,generate,analyze}.md` —
  the cypilot waterfall this proposal wraps as `stories/cypilot/`.
  The `cpt` CLI keeps running underneath; only orchestration moves.
- `cyber-repo/tools/loopy/pr-refine.py` — the existing PR refinement
  pipeline that becomes `stories/pr-refinement/` after migration.
- `ideas.md` — backlog items "extensible stories", "ticket/pr/etc...
  providers", "providers behind mcp", "file story bug or kitsoki bug",
  and "LLM-driven local review checklist" all cash out through this
  proposal.
