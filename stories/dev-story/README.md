# dev-story — engineer's-day hub

Wave 2 / Phase 2 of the dev-story / bugfix unify proposal (§5.1). The
GENERAL-PURPOSE app that imports `stories/bugfix/` and
`stories/pr-refinement/` and routes between them via day-level rooms
(landing, inbox, ticket_search, workspace_manager, standup, agent,
code_review, deploy, observability, incident, docs). The root is the
free-form workbench [`landing`](#the-free-form-workbench-landing), which
replaced the former `main` catalog.

This app does **not** bind providers. Concrete bindings happen at the
instance level: a generated `.kitsoki/` wrapper or an importing project app
selects ticket, git/workspace, CI, and transport providers for the target
repository. The canonical shape is a small project-owned wrapper that
imports `@kitsoki/dev-story` under `core`, declares the full strict host
allow-list, binds `ticket` / `vcs` / `ci` / `workspace` / `transport`, and
projects project defaults through `world_in`. Project onboarding writes that
wrapper under `.kitsoki/stories/<project>-dev/`; `.kitsoki/stories/kitsoki-dev/`
is the self-hosted golden example of the same contract.

Standalone:

```
kitsoki run stories/dev-story/app.yaml
```

The defaults in `host_interfaces:` (host.local_files.ticket, host.git,
host.local, host.git_worktree, host.append_to_file) make standalone
runs work for smoke testing without an instance wrapper.

## Composition

```
dev-story
  ├── imports bf  (../bugfix)
  │     entry: idle
  │     world_in: ticket_id, ticket_title, workdir, feature_branch,
  │               base_branch, judge_mode, …
  │     exits:
  │       done    → pr (with pr_title / pr_body lifted from
  │                     bf__done_artifact.summary_{title,markdown})
  │       abandoned → landing (status: "abandoned")
  │
  ├── imports pr  (../pr-refinement)
  │     entry: open_pr   # skip pr-refinement's standalone-only idle
  │     world_in: ticket_id, workdir, feature_branch, base_branch,
  │               pr_title, pr_body, judge_mode, …
  │     exits:
  │       merged          → landing (status: "merged", last_pr_url=pr__pr_url)
  │       abandoned       → landing (status: "abandoned")
  │       pushback_resolved → landing (Wave 3 reserves; Wave 2 maps to landing)
  │
  └── imports prd (../prd)             # the front of the PRD → Design walk
        entry: idle
        world_in: workdir, judge_mode, judge_confidence_threshold
        exits:
          done      → prd_published    # landing room; carries the PRD into design
          abandoned → landing (status: "abandoned")

  └── imports tests (../fix-tests)      # make-test-green loop
        entry: starting
        world_in: test_cmd, quick_test_cmd, quick/full timeouts, workdir, max_cycles
        exits:
          achieved    → landing (status: "tests-green")
          exhausted   → landing (status: "tests-exhausted")
          needs-human → human_review_report
```

The bf → pr handoff is one import edge. When bf fires `@exit:done` the
runtime evaluates dev-story's `imports.bf.exits.done` projection in bf
scope (writing `world.pr_title` / `world.pr_body` in the parent), then
transitions into `pr` — whose compound OnEnter runs the pr `world_in:`
setters in parent scope to project those keys into `pr__<key>` (which
pr's own rooms then reference). The full chain is exercised by
`flows/bugfix_to_pr.yaml`.

The `tests` import is a self-driving command repair loop. `go_fix_tests` enters
it from landing through the child's reset entry, runs a bounded quick gate
(`quick_test_cmd`, falling back to `test_cmd`) for repair cycles, proves the full
`test_cmd` gate (falling back to `make test`), then loops fixer -> quick gate ->
full gate -> review until both gates pass. It rejects green diffs that weaken
tests or lose functionality. It is exercised by
[`flows/fix_tests_autonomous.yaml`](./flows/fix_tests_autonomous.yaml).

## PRD → Design walk

`landing → prd → (publish) → prd_published → continue → design` is the
discovery-to-design walk. From `main`, `prd` enters the imported
[`stories/prd/`](../prd/) discovery pipeline (idle → clarifying → brief →
references → drafting). When the operator accepts, prd publishes the PRD
to `docs/prd/<slug>.md` and fires `@exit:done`; dev-story lands in the
**`prd_published`** room ([`rooms/prd_published.yaml`](./rooms/prd_published.yaml)),
which confirms the published path and offers two arcs:

- **`continue`** → the **design** intake, seeding `design_seed_idea` with
  a pointer to the just-published PRD (`"Author a design from the PRD at
  <prd_file>"`) so the design author reads it as prior art.
- **`go_main`** → back to the hub.

`prd_file` is a host **bind** in prd's drafting accept arc (it comes from
`prd_publish.star`), so it commits post-dispatch — too late for a
synchronous exit `set:` projection to carry it (contrast bf → pr, whose
carried `done_artifact` is a synchronous `set:`). The flat world keeps
`prd__prd_file` once the turn settles, so `prd_published` reads
`world.prd__prd_file` directly. prd stays runnable standalone
(`kitsoki run stories/prd/app.yaml`) — the redirect lives only in
dev-story's composition. The walk is exercised by
[`flows/prd_to_design.yaml`](./flows/prd_to_design.yaml).

## Doc profile — targeting an external project

The PRD -> Design walk above publishes into the default project docs paths,
but the *document shape* and *placement* are a **profile** an instance app can
override - no engine or room change needed. An instance
points the same hub at another repository or at a project-specific docs tree
purely by setting world keys: different templates, fixed filenames, local file
tickets, GitHub issues, or no follow-up ticket at all. External targets live in
their **own** repo as a zero-config `stories/<name>/` instance, discovered by
the default `./stories` walk, importing this base via `@kitsoki/dev-story` from
the binary's embedded story library - see
[`docs/web/tour.md`](../../docs/web/tour.md) for the broader
kitsoki-as-a-dependency story (shipped; the design proposal was retired).

The profile is the "External-target profile" world block in
[`app.yaml`](./app.yaml) (search `External-target profile`). Every key has
a shared default - **overriding them is the profile**:

Project-onboarding generated instances override these shared defaults for
generic repositories: PRDs go to `.context/prd`, design documents go to
`.context/designs`, `design_template_dir` is empty, and no feature ticket is
minted unless the local profile opts in. Project profiles can still keep
repo-native docs paths such as `docs/prd` and `docs/proposals`.

| World key | Default | Effect |
|---|---|---|
| `repo_root` | `""` | external checkout root; threaded into every `iface.ticket.*` call as the `root` arg, so `host.local_files.ticket` scans `<repo_root>/issues/...` (`""` ⇒ cwd). `host.gh.ticket` ignores it (its source is `ticket_repo`). |
| `publish_durable_path` | `docs/prd` | PRD publish home (relative to `workdir`); projected into the `prd` import via `world_in`. |
| `prd_doc_filename` | `""` | fixed PRD filename (e.g. `PRD` → `PRD.md`); `""` ⇒ slug-named (`<slug>.md`) |
| `prd_mockup_path` | `""` | optional project-local HTML mockup embedded into the PRD published read-out; project demos set this, ordinary repos leave it empty. |
| `design_template_dir` | `docs/proposals/templates` | dir the design author reads its doc templates from |
| `design_durable_path` | `docs/proposals` | DESIGN publish home (relative to `workdir`). |
| `design_doc_filename` | `""` | fixed DESIGN filename (e.g. `DESIGN` → `DESIGN.md`); `""` ⇒ slug-named |
| `design_ticket_dir` | `issues/features` | where the linking feature ticket is minted; `""` ⇒ **skip** minting (an external target tracks work elsewhere, e.g. GitHub issues) |
| `ticket_repo` | `""` | active selected GitHub issue repo. Generated wrappers keep this empty at rest so local/pasted reports stay local; `ticket_search` fills it after a GitHub row is picked. Non-empty during design publish mints a GitHub feature issue instead of a local file. |
| `ticket_github_repo` | `""` | GitHub issue source shown beside local artifact tickets when the wrapper binds `ticket` to `host.local_github.ticket`; usually an `owner/repo` slug or a resolvable remote name. |

How the keys reach the glue: the `prd` import's `world_in` projects
`publish_durable_path` + `prd_doc_filename` into the prd child;
[`rooms/design_draft.yaml`](./rooms/design_draft.yaml) passes the
`design_*` keys to `publish_design.py` and threads `design_template_dir`
into the author prompt (`prompts/design_draft.md` reads
`{{ args.template_dir }}`).

The placement seam is the two publish scripts, which take optional
positional args:

- [`stories/prd/scripts/prd_publish.star`](../prd/scripts/prd_publish.star)
  publishes to the `durable` home relative to `workdir`; a non-empty
  `doc_filename` overwrites a **fixed** `<durable>/<doc_filename>.md`
  instead of `<durable>/<slug>.md`.
- [`stories/dev-story/scripts/publish_design.py`](./scripts/publish_design.py)
  `… [workdir] [durable] [doc_filename] [ticket_dir]` — same `workdir` /
  `durable` / `doc_filename` contract, plus `ticket_dir`: a non-empty
  value mints the kitsoki feature ticket there (`issues/features` by
  default); an **empty** `ticket_dir` skips ticket minting entirely.

Project-specific placement is expressed as a plain relative directory plus an
optional fixed `doc_filename` override — there is no placement enum. The clean
layering is: community/shared patterns live in dev-story, organization or
project conventions live in the importing instance's profile, and local
exceptions are explicit world defaults in that instance.

## Import Wrapper Contract

Generated project wrappers and the self-hosted `kitsoki-dev` wrapper use this
shape:

- `routing.free_form_fallback` points `core.*` rooms back to
  `core.landing` through `core__landing_capture`, so free text works from
  menu-shaped rooms after import aliasing.
- `hosts:` declares the full strict surface used by dev-story and its child
  imports, including ticket providers, agent verbs, IDE/diff helpers, chat,
  Starlark, and local command hosts. Strict parents should prefer this superset
  over hand-pruning; unused entries are harmless, missing entries break loads.
- `host_bindings.ticket` is `host.local_files.ticket` for good local capability
  and `host.local_github.ticket` when a GitHub source is configured. The latter
  searches local artifacts and GitHub issues side by side while routing selected
  close-out by source.
- `world_in` always projects `workdir`, `repo_root`, judge settings, document
  placement, `prd_mockup_path`, `ticket_repo`, `ticket_github_repo`,
  `bugfix_destination`, and project build/test commands.
- At rest, `ticket_repo` is empty and `ticket_github_repo` carries the configured
  GitHub source. This is the key distinction that lets a project have full
  GitHub search/fetch/close-out for remote issues without making pasted or local
  bug reports mutate GitHub.

The renderer for onboarding-generated wrappers lives in
[`scripts/init_apply.py`](./scripts/init_apply.py). Keep new import-surface
requirements there first, then mirror only deliberate project-specific defaults
in examples such as `stories/slidey-dev/`.

## Provider Neutrality

The legacy `testdata/apps/dev-story/` stub had Jira-flavoured world
keys (`jira_query`, `jira_results`) and called `host.run` with hard-
coded `echo` commands. dev-story (this app) strips those:

| Legacy | Provider-neutral |
|---|---|
| `world.jira_query` | `world.ticket_query` |
| `world.jira_results` | `world.ticket_results` |
| `host.run` (echo) | `iface.ticket.search` / `iface.ticket.list_mine` |

An importing project can bind `iface.ticket` to local files, a composite
local+GitHub provider, or another provider that implements the same interface.
Same room YAML, different provider.

### Parent meta-repo ticket providers

Project onboarding supports the common meta-repo / monorepo layout where a
parent checkout owns shared auth and MCP setup while child projects live under
`projects/` or `src/`. If the child has no GitHub `ticket_repo`, discovery
walks ancestor directories for `.kitsoki/project-profile.yaml` and inherits a
non-GitHub `tracker.provider` plus `kitsoki.instance.bindings.ticket`.

Example parent profile shape:

```yaml
tracker:
  provider: meta-jira
  repo: PLATFORM
  setup_command: ./scripts/dev-env jira
  readiness_command: ./scripts/dev-env jira --check
  required_env: [JIRA_URL, JIRA_USERNAME, JIRA_API_TOKEN]
kitsoki:
  instance:
    bindings:
      ticket: .kitsoki/providers/meta_jira_ticket.star
```

The provider script is a `ticket_provider/v1` module: it defines pure Starlark
functions named after the ticket interface operations (`search(ctx)`, `get(ctx)`,
`comment(ctx)`, `transition(ctx)`, `list_mine(ctx)`, plus optional extended
ops). It does **not** define `main(ctx)`. The sidecar beside the script owns HTTP
capability and symbolic auth policy:

```yaml
kind: ticket_provider/v1
http:
  methods: [GET, POST]
  hosts: [tickets.example.internal]
auth:
  jira:
    env: JIRA_API_TOKEN
    header: Authorization
    prefix: "Bearer "
    missing_code: missing_jira_token
```

The script calls `ctx.http.get/post(..., auth="jira")`; the Go HTTP transport
reads the env/secret source and injects the request header after Starlark has
built the request, so token values are never Starlark values. Provider functions
may return structured failures with `{"ok": False, "error": {"code": "...",
"message": "..."}}`, which lets an instance distinguish cases such as an
expired access token versus a missing forge PAT.

The generated child profile stores the inherited provider metadata, rebases
the `.star` binding path relative to the child checkout, and adds the readiness
command to `.kitsoki/check-readiness.py` when declared. `ticket_repo` remains
GitHub-only; private providers bind through `iface.ticket` and keep
`ticket_repo: ""` so GitHub-specific publish/closeout paths do not run.

## Rooms

| Room | Status | Notes |
|---|---|---|
| `landing` | **root** | The **free-form workbench** — dev-story's root, replacing the former `main` catalog ([freeform-landing](#the-free-form-workbench-landing) below). A full-tool, Claude-Code-like agent (`landing_agent`) is the resting surface: the operator describes work in their own words (the `landing_capture` intent → on_enter `host.agent.task`, via the [`workbench:`](../../docs/architecture/room-workbench.md) primitive), read-only by default and gated to a write-mode opt-in (`write_mode: read_only`). Carries `main`'s highest-value navigation forward as quick actions + intents. Declares the [agent off-ramp](../../docs/stories/state-machine.md#11-off-path-the-global-escape-hatch) (`workbench.off_ramp_agent: agent_qa`) as its read-only Q&A floor. Every pipeline returns here (`go_main`/`go_back` self-loop). |
| `applying` | — | The deterministic executor for an accepted ad-hoc [plan](#ad-hoc-structured-plan-proposeacceptrefineapplyverify): re-prompts the `landing_agent` with the **accepted plan as instruction** (`prompts/apply.md`) and binds a **distinct `apply_note`** (binding `landing_note` would let `once:` skip the dispatch), then emits `run_verify`. |
| `verifying` | — | Runs the accepted plan's **verify gate** (`host.starlark.run` script and/or `gate_reviewer` agent), binds tri-state `verify_ok`, and routes on the post-bind verdict: PASS → `plan_done`, FAIL → `landing` (`last_error` = the gate's reason). Pinned `decider: llm` so the deterministic verdict auto-fires in STAGED mode. |
| `plan_done` | — | The plan completion read-out (`captured++`); `go_main`/`go_back` return to the workbench. |
| `ticket_search` | Wave 2 | iface.ticket.search; picks a ticket, then `drive` routes by `ticket_type` (bug → bf, feature → impl, epic → cyp). `pick_ticket` reads the type off the picked row; `go_bugfix` forces bf regardless of type. |
| `workspace_manager` | Wave 2 | iface.workspace.list. Minimal Wave 2 shape. |
| `inbox` | Wave 2 | Navigation surface; the runtime's inbox subsystem manages items. |
| `agent` | Wave 2 | One-shot ask_question via `host.agent.ask` (agent: `agent_qa`). |
| `standup` | Wave 2 | Aggregates iface.ticket.list_mine. |
| `design*` | — | **Design pipeline** (formerly the "proposal" pipeline): discovery+brief (one room: the first message mints the workspace + scaffolds an editable brief, then every turn converses + distils it; `ready` runs the quality judge and a passing brief auto-advances) → existing-state → completeness → references → draft → publish (to `docs/proposals/<slug>.md`). **Publish also files a feature ticket** (`issues/features/`) linking back to the design doc, and `design_done` offers a **decompose-vs-direct** choice on it (never a size heuristic — an operator pick): `implement` (the `go_implementation` intent) drives that ticket straight into the impl pipeline (`flows/design_to_implementation.yaml`) — no detour through `ticket_search`; `decompose` (the `go_deliver` intent) instead drives the just-published proposal into [`stories/deliver`](../../docs/stories/deliver.md), the canonical decomposition story, which fans `stories/fleet` over the resulting briefs (`flows/design_to_decompose_to_impl.yaml`). Both arcs land back on `landing` (`status="delivered"` or `"merged"`) or `@exit:needs-human`. The design pipeline does not create a worktree; `impl.idle.on_enter` self-provisions it on entry (mirroring `bf.idle`), so the impl run gets a real `feature/<ticket>` branch regardless of entry path. Reached ad-hoc via `idea`, or as the back half of the [PRD → Design walk](#prd--design-walk). |
| `prd_published` | — | PRD → Design landing room (see [PRD → Design walk](#prd--design-walk)). |
| `ideas` | — | Ideas-backlog reviewer (see below). |
| `code_review` | Wave 3 stub | Reserves the room; imports `stories/code-review/` in Wave 3. |
| `incident*` | Wave 3 | **On-call response loop** — alert → triage → mitigate \| escalate \| monitor → postmortem ([incident-response](#incident-response-loop-incident) below). |
| `deploy*` | Wave 3 | **Release loop** — target → preflight gate → ship → verify \| rollback ([deploy](#deploy-loop-deploy) below). |
| `observability*` | Wave 3 | **Monitoring loop** — signal → query → triage → alert \| annotate \| clear ([observability](#observability-loop-observability) below). |
| `docs*` | Wave 3 | **Documentation loop** — target → draft → review → publish \| revise ([docs](#docs-loop-docs) below). |

### Incident-response loop (`incident`)

Reached from `landing` via `go_incident`. The former dead-end stub is now a real
**on-call response loop** ([`rooms/incident.yaml`](./rooms/incident.yaml)): the
engineer pastes a production alert, the on-call agent triages it, and the loop
routes deterministically on the recommendation.

```
incident (intake) ── report_incident ──▶ incident_triaging
                                           │ on_enter host.agent.decide
                                           │ binds incident_triage (severity +
                                           │ summary + recommendation)
                                           ▼
                                       incident_triaged  (the verdict)
                                           ├─ mitigate ▶ incident_mitigating ─▶ incident_resolved
                                           ├─ escalate ▶ incident_escalating ─▶ incident_resolved
                                           └─ watch    ▶ incident (parked, status=monitoring)
                                       incident_resolved
                                           └─ write_postmortem ▶ incident_postmortem ─▶ landing
```

- **Triage** runs `host.agent.decide` (`gate_reviewer` persona,
  `prompts/incident_triage.md`, `schemas/incident-triage.json`) and binds a
  `{ severity (sev1|sev2|sev3), summary, recommendation, suspected_cause,
  mitigation }` verdict. The recommendation is unset until the decide binds, so
  the post-bind guarded emits **auto-route** on it (the cherny decision-emit
  discipline) — and the room is `decider: llm`-pinned so STAGED mode (kitsoki
  web) fires the deterministic route instead of stalling for a human.
- **Mitigate** runs the recorded mitigation action (`host.run` argv mode — an
  instance rebinds this to a real runbook executor) and advances to the
  resolved read-out.
- **Escalate** posts the page out-of-band (`iface.transport.post` — default
  `host.append_to_file`, an instance rebinds to PagerDuty / Slack) and mirrors
  it into the operator's inbox (`host.inbox.add`).
- **Watch** parks the alert back at intake (`incident_status=monitoring`); a
  fresh `report_incident` re-arms triage cleanly.
- **Postmortem** runs the `landing_agent` (`host.agent.task`,
  `prompts/incident_postmortem.md`, `schemas/incident-postmortem.json`) under
  the same read-only → write-mode opt-in posture as `applying.yaml`, writing a
  blameless write-up to `docs/incidents/`.

Every host the loop touches is already in dev-story's allow-list or is an iface
default, so the whole loop is **no-LLM-gateable**: the three flow fixtures
(`incident_mitigate` / `incident_escalate` / `incident_monitor_park`) stub the
two agent calls and assert the deterministic routing — severity-based
dispositions, the recorded mitigation action, the page + inbox mirror, the
postmortem write — with no real LLM.

### Deploy loop (`deploy`)

Reached from `landing` via `go_deploy`. The former dead-end stub is now a real
**release loop** ([`rooms/deploy.yaml`](./rooms/deploy.yaml)): the engineer names
a deploy target, the loop runs preflight checks, an agent gates the result
(go/no-go), and on ship it deploys, verifies the new release, and rolls back
automatically if the probe is red.

```
deploy (intake) ── start_deploy ──▶ deploy_preflighting
                                      │ on_enter host.run preflight
                                      │ + host.agent.decide gate (go|no_go)
                                      ▼
                                  deploy_gated  (the verdict)
                                      ├─ ship   ▶ deploy_shipping ─▶ deploy_verifying
                                      └─ cancel ▶ deploy (parked, status=cancelled)
                                  deploy_verifying
                                      │ on_enter host.run verify-probe
                                      │ + host.agent.decide health (healthy|unhealthy)
                                      ├─ healthy   ▶ deploy_succeeded ─▶ landing
                                      └─ unhealthy ▶ deploy_rolling_back ─▶ deploy_succeeded ─▶ landing
```

- **Preflight gate** runs the recorded preflight (`host.run` argv mode — an
  instance rebinds to a real test/working-tree/migration check) then
  `host.agent.decide` (`gate_reviewer`, `prompts/deploy_gate.md`,
  `schemas/deploy-gate.json`) and binds a `{ verdict (go|no_go), summary,
  blocking }` verdict. The verdict auto-routes via post-bind guarded emits (the
  cherny discipline); `decider: llm`-pinned for STAGED mode. The `ship`
  affordance is hidden on a `no_go` gate.
- **Ship** runs the recorded deploy action (`host.run`) and advances to
  verification — the red-after-green discipline: prove the release is good,
  don't assume.
- **Verify** runs the recorded probe (`host.run`) + a health gate
  (`host.agent.decide`, `schemas/deploy-health.json` → `verdict
  (healthy|unhealthy)`) and routes: healthy → succeeded; unhealthy →
  rolling back. The rollback action clears `deploy_action` on entry so its
  `once:` guard re-arms (the ship step already bound it).
- **Cancel** parks the deploy back at intake; a fresh `start_deploy` re-arms
  the preflight + gate cleanly.

Three flow fixtures (`deploy_succeed` / `deploy_rollback` / `deploy_no_go`) stub
the two decides and assert the deterministic routing — the go/no-go gate, the
recorded ship action, the health-based succeed/rollback split, the no-go park —
with no real LLM. The host.run steps run REAL `printf` so the recorded actions
are asserted for real.

### Observability loop (`observability`)

Reached from `landing` via `go_observability`. The former stub is now a real
**monitoring loop** ([`rooms/observability.yaml`](./rooms/observability.yaml)):
the engineer names a signal / dashboard, the loop queries it, an agent triages
the reading, and the loop routes on the disposition.

```
observability (intake) ── query_signal ──▶ observability_querying
                                             │ on_enter host.run query
                                             │ + host.agent.decide triage
                                             ▼
                                         observability_triaged  (the verdict)
                                             ├─ raise_alert ▶ observability_alerting   ─▶ observability_done
                                             ├─ annotate    ▶ observability_annotating ─▶ observability_done
                                             └─ clear        ▶ observability (parked, status=clear)
```

- **Triage** runs the recorded query (`host.run`) then `host.agent.decide`
  (`gate_reviewer`, `prompts/obs_triage.md`, `schemas/obs-triage.json`) and
  binds a `{ disposition (alert|annotate|clear), summary, detail }` verdict. The
  disposition auto-routes via post-bind guarded emits; `decider: llm`-pinned.
- **Alert** posts the signal out-of-band (`iface.transport.post` — default
  `host.append_to_file`, an instance rebinds to PagerDuty / Slack) and mirrors
  it into the inbox (`host.inbox.add`).
- **Annotate** runs the recorded annotation action (`host.run` — an instance
  rebinds to a dashboard-annotation API) and does NOT page.
- **Clear** parks the signal at intake; a fresh `query_signal` re-arms triage.

Three flow fixtures (`observability_alert` / `observability_annotate` /
`observability_clear_park`) stub the decide and assert the deterministic routing
— the out-of-band alert + inbox mirror, the recorded annotation, the clear park
+ re-triage — with no real LLM.

### Docs loop (`docs`)

Reached from `landing` via `go_docs`. The former stub is now a real
**documentation loop** ([`rooms/docs.yaml`](./rooms/docs.yaml)): the engineer
names a doc target, the writer drafts it, the operator reviews the close-out
note and either publishes (announces it out-of-band) or revises (re-drafts).

```
docs (intake) ── draft_doc ──▶ docs_drafting
                                 │ on_enter host.agent.task (write_mode read_only)
                                 │ binds docs_draft { summary, file_path, headings }
                                 ▼
                             docs_review  (the close-out note)
                                 ├─ publish_doc ▶ docs_publishing ─▶ docs_published ─▶ landing
                                 └─ revise_doc  ▶ docs (re-draft, status=revising)
```

- **Draft** runs the doc writer (`host.agent.task`, `landing_agent`,
  `prompts/docs_draft.md`, `schemas/docs-draft.json`) under the same read-only →
  write-mode opt-in posture as `incident_postmortem` / `applying` — headless it
  stays read-only and reports what it would write. Binds the close-out note and
  advances to review.
- **Publish** announces the doc out-of-band (`iface.transport.post`) and lands
  on the published read-out.
- **Revise** parks back at intake (`status=revising`, the prior draft retained
  so the operator sees what they're revising); a fresh `draft_doc` clears the
  draft and re-arms the writer.

Two flow fixtures (`docs_publish` / `docs_revise`) stub the writer task and
assert the deterministic routing — the draft bind, the publish announcement, the
revise re-arm — with no real LLM.

### The free-form workbench (`landing`)

`landing` is dev-story's **root** — the resting *floor* of the engineer's day,
replacing the former `main` catalog. Where `main` was a menu hub (name a ticket,
search, pick, then run), `landing` is a full-tool agent that behaves like Claude
Code: the operator describes what they want in their own words and the
`landing_agent` (`app.yaml agents:`, full toolbox: `Read, Grep, Glob, Edit,
Write, Bash`) picks it up. The pipelines (bf / impl / cyp / pr / rev / prd →
design) are the **grown structure reached from** the floor, carried forward as
quick-action buttons and intents so nothing `main` offered is lost; every
pipeline's exit returns here, and `go_main` / `go_back` self-loop the workbench.

**Built on the `workbench:` primitive**
([`docs/architecture/room-workbench.md`](../../docs/architecture/room-workbench.md);
landing is its reference consumer). The room declares one `workbench:` block
instead of hand-rolling `write_mode` + `agent_off_ramp` + an `on_enter
host.agent.task` + a free-text capture arc; the loader desugars it at load time
into exactly those four primitives, wired to `landing_agent`'s WS `toolbox:` +
`effect:` declaration (`app.yaml agents.landing_agent`, `toolboxes.landing_toolbox`
— `effect: write`, not the legacy `tools:`/`bash_profile:`/`external_side_effect:`
triplet).

**Read-only by default → opt into write.** `workbench:` sets `write_mode:
read_only`; the agent boots with its full toolbox but every *mutating* tool
call (`Edit` / `Write` / side-effecting `Bash`) holds for an operator
write-mode grant before the effect lands, recorded as a
`machine.write_mode_granted` event; headless (cassettes / flows / no operator)
the gate denies and the agent stays read-only. The gate is
[`internal/host/write_mode_gate.go`](../../internal/host/write_mode_gate.go);
the landing is its first real client (the `agent-write-mode-opt-in` slice).
`landing_agent`'s WS `effect: write` (not `external`) mirrors
`ExternalSideEffect=false`, which is what keeps the static and runtime
postures in agreement.

The agent turn fires on the **`landing_capture`** intent (slot `request`) -
named to match `workbench:`'s fixed `<room>_capture` synthesis, not the macro's
plain synthesized arc: `landing_capture`'s `on:` transition is hand-authored so
it can carry the onboarding-command escape hatch and thread continuity
(preserving the prior note/plan across the re-dispatch) that the macro does not
(and should not) auto-generate. It captures the operator's utterance, clears
the prior note to re-arm, and loops back so `on_enter` dispatches
`host.agent.task` (`agent: landing_agent`, `acceptance.schema:
schemas/landing-note.json` — a minimal, permissive close-out note: the engine
requires *a* schema on `task`, so "free output" is expressed as a one-field
`summary` with `additionalProperties` open, plus `workbench.context_args` to
thread `prior_summary`/`prior_details`/`prior_plan` into the prompt). Because
the app declares no explicit `routing:` block naming `landing`/`landing_capture`,
every other non-conversational room falls back to it via
`routing.free_form_fallback` auto-detection
([`semantic-routing.md` §1.6](../../docs/architecture/semantic-routing.md)) — an
instance importing dev-story under an alias (e.g. `core`) must declare that
block explicitly instead, since the auto-detect's own heuristic only ever
matches the un-aliased bare name (see `stories/pets-dev/app.yaml` and
`stories/slidey-dev/app.yaml`). Free
text the router can't map to an action is answered in place by the read-only
**agent off-ramp** (`workbench.off_ramp_agent: agent_qa`) — the same floor
`main` declared. The `world.captured` counter (rendered read-only here) is the
progressive-determinism read-out, incremented by the mining apply path.

#### Ad-hoc structured plan (propose→accept/refine→apply→verify)

When the request is concrete, actionable work, the `landing_agent` proposes a
**validated, executable `plan`** (one goal, one run-then-verify step, a Starlark
verify gate) in its close-out note instead of prose. The workbench renders a
reviewable **plan card**; the operator **Accepts** it (or types an adjustment to
**refine** it — the `landing_capture` sink re-dispatches the planner with the
prior plan as context), `apply` runs the step under the write-mode grant, then the verify gate
proves it landed and routes on a **real pass/fail verdict**. The full narrative —
the rooms (`applying` / `verifying` / `plan_done`), the plan schema (a strict
subset of cherny-loop's `gate_plan`), the Starlark read-only inspection
capability, and the no-LLM flow fixtures — is
[docs/stories/ad-hoc-plan.md](../../docs/stories/ad-hoc-plan.md).

### Ideas reviewer (`ideas`)

Reached from `main` via `ideas`. Reconciles the hand-maintained ideas backlog
(`world.ideas_path`, default repo-root `ideas.md`, with `## Done` /
`## Partial / in progress` / `## Ideas` sections) against work that has actually
shipped. `on_enter` runs the read-only `ideas_reviewer` agent against the repo
root — it reads the backlog, the commit history (`git log`), and the docs
(especially `docs/proposals/`) and proposes section **moves**, each backed by
concrete evidence, plus a few high-value **candidates** worth proposing next.

The decide is interpretation; the mutation is deterministic. `apply` is a
confirm gate: it hands the persisted report to `scripts/ideas_reconcile.star`,
which rewrites the backlog file (the same decide→script discipline as the
design slug step). `pick N` seeds `world.design_seed_idea` from candidate N
and jumps into the `design` intake — so a blocked author flows straight into
authoring a design doc (slug + workspace minting is reused as-is). `regenerate`
re-scans the rewritten backlog.

## Intent surface

Day-level intents live in this app's `intents:` block. Importing
overlapping bare names from bf and pr is impossible (the loader
rejects collisions); the operator types prefixed forms (`bf__accept`,
`pr__proceed`) when inside an imported sub-story. Imported bare-name
intents in Wave 2:

| From | Lifted to bare name |
|---|---|
| `bf` | `start` |
| `pr` | `open`, `monitor`, `retry`, `resolve`, `merge_now` |

The parent declares additional navigation / pipeline-launching
intents at the bare name: `landing_capture` (the free-form workbench request —
slot `request`), `go_main` / `go_back` (now self-loop the `landing`
floor), `go_inbox`, `go_agent`, `go_ticket_search`,
`go_workspace_manager`, `go_standup`, `go_code_review`, `go_deploy`,
`go_observability`, `go_incident`, `go_docs`, `go_bugfix`,
`bugfix_report` (inline free-text bug complaint), `bugfix_ref` (local/remote
ticket id lookup), `bugfix_link` (explicit issue-link lookup),
`go_pr_refinement`, `search_tickets`, `pick_ticket`, `ask_question`,
`summarize_day`, `proceed`, `quit`, `look`. The incident loop adds
`report_incident` (slot `alert`), `mitigate`, `escalate`, `watch`, and
`write_postmortem` (the three dispositions + the two button-only disposition
verbs are scoped to the incident rooms). The deploy loop adds `start_deploy`
(slot `target`), `ship`, `cancel_deploy`; the observability loop adds
`query_signal` (slot `signal`), `raise_alert`, `annotate_signal`,
`clear_signal`; the docs loop adds `draft_doc` (slot `target`), `publish_doc`,
`revise_doc` (each loop's disposition verbs are button-only and scoped to its
rooms; the post-bind guarded emit auto-routes on the agent's verdict).

## Flows

| Flow | Coverage |
|---|---|
| `landing_smoke.yaml` | Boot, land in the free-form workbench (`root: landing`), render view, `go_main` self-loops the floor. Smallest possible smoke (replaces `main_smoke`). |
| `landing_quick_action.yaml` | From `landing` a quick action (`go_ticket_search`) reaches ticket_search → search → pick → `drive` routes into the bugfix pipeline. Proves the re-homed navigation is intact (replaces `ticket_search_smoke`). |
| `landing_off_ramp.yaml` | The read-only Q&A floor: an unmapped utterance never advances the workbench and never mutates world (the invariant the live off-ramp converse rests on; the converse answer itself is the LLM step, exercised by the web posture + `offramp_test.go`, never CI). |
| `landing_write_mode_opt_in.yaml` | The `landing_capture` intent captures a (mutating) request and re-arms the on_enter `landing_agent` task (stubbed); the workbench stays put as the read-only floor. The gate's decision spine (mutating-step classify, grant scopes, headless deny, recorded event) is unit-tested end-to-end in `internal/host/write_mode_gate_test.go` (a flow stub bypasses the in-subprocess gate, per AGENTS.md). |
| `pickup_to_bugfix.yaml` | landing → ticket_search → pick → dispatch into the bf import (lands in bf.idle with world_in: projections firing). |
| `github_ticket_drive_routes.yaml` | `iface.ticket` rebound to `host.gh.ticket`: a GitHub-Issue-sourced bug carries a provider-classified `ticket_type` (from its `bug` label), so a row pick (`n=`) lands `ticket_type=bug` and the headline `drive` routes into bf — no silent self-loop. The get also surfaces `source=github` + the lifted `legacy_id`, so the local↔issue identity shows in the ticket view. Regression for the two `host.gh.ticket` provider bugs. |
| `bugfix_pasted_report_intake.yaml` | `bugfix_report` starts the bugfix child from an inline complaint, projecting it as `ticket_body` with `ticket_source_mode=freeform`. |
| `bugfix_bare_command_pick_ticket_guard.yaml` | Bare `fix bug` is handled by `landing_capture` at runtime and prompts for a picked ticket, avoiding a broad semantic example that would steal inline reports. |
| `bugfix_ref_local_intake.yaml` | `bugfix_ref` resolves a local ticket id through `iface.ticket.get` before entering the bugfix child, preserving the local artifact path as `thread`. |
| `bugfix_ref_raw_capture_intake.yaml` | `landing_capture` preserves a raw `fix bug <id>` phrase and resolves the exact local id before entering the bugfix child. |
| `bugfix_ref_remote_intake.yaml` | `bugfix_ref` resolves a remote issue number through the configured ticket provider before entering the bugfix child, so triage sees the provider body/title/url. |
| `bugfix_link_provider_intake.yaml` | `bugfix_link` resolves an explicit issue URL through the configured ticket provider before entering the bugfix child. |
| `bugfix_link_raw_capture_intake.yaml` | `landing_capture` preserves a raw `fix issue link <url>` phrase and resolves the exact issue URL before entering the bugfix child. |
| `bugfix_to_pr.yaml` | The full closed-loop walk: landing → bf.idle → walk every bf room to @exit:done → handoff into pr → walk pr to @exit:merged → land back in `landing` with status="merged" and last_pr_url populated. |
| `fix_tests_autonomous.yaml` | `go_fix_tests` enters the imported make-test-green loop, drains quick/full deterministic gates plus review, and projects the report/review back to landing. |
| `fix_tests_resets_stale_child_world.yaml` | Seeds stale `tests__*` child state before `go_fix_tests`; proves the quick action starts a fresh cycle-0 review instead of inheriting old failure/review state. |
| `design_to_implementation.yaml` | The publish → implement bridge: design_done → `go_implementation` → impl.idle (on_enter self-provisions the worktree — the fixture seeds NO workspace) → walk the impl pipeline to @exit:done → `landing` with status="merged". |
| `design_to_decompose_to_impl.yaml` | The publish → decompose bridge, sibling to `design_to_implementation.yaml`: design_done → `go_deliver` → `deliver.configure` → `deliver__start` → decompose (agent mocked) → lint (Starlark) → review (agent mocked, accept) → fleet fan-out over a 2-brief fixture (reuses deliver's own `rich_schema_happy` manifest) → `@exit:done` → `landing` with status="delivered". Also proven live (no LLM) on the web (`tools/runstatus/tests/playwright/deliver-decompose-walk.spec.ts`) and VS Code (`tools/vscode-kitsoki/tests/vscode-deliver-decompose-walk.e2e.spec.ts`) surfaces — see [`docs/stories/deliver.md`](../../docs/stories/deliver.md). |
| `deliver_router_picks_arc.yaml` | decompose-vs-direct is an OPERATOR CHOICE, never a size heuristic: four sub-fixtures prove the SAME published proposal routes to `impl` on `go_implementation` and to `deliver` on `go_deliver`, from both `design_done` and `landing`, plus the `landing` guard when no proposal is published yet. |
| `prd_to_design.yaml` | The PRD → Design walk: landing → `go_prd` → walk the imported prd pipeline to @exit:done → land in `prd_published` (prd__prd_file lifted) → `continue` → the `design` intake, seeded with a pointer to the published PRD. |
| `plan_propose_render.yaml` | A stubbed planner returns a note *with* a plan → the [ad-hoc plan](#ad-hoc-structured-plan-proposeacceptrefineapplyverify) card + Accept & apply quick action render; `look` re-renders without re-dispatching. |
| `plan_refine.yaml` | A free-text adjustment re-uses the `landing_capture` sink: the prior plan is preserved into `landing_plan_prior` (fed into the re-dispatched prompt) and a revised plan binds — asserts the *dispatched prompt* carries the prior plan. |
| `plan_apply_verify_green.yaml` | accept → apply → the **real** verify script runs against an inspect cassette (3 ≥ 3) → `{ok:true}` → `plan_done`, `captured++`. Exercises `ctx.probe` on the happy path. |
| `plan_apply_verify_red.yaml` | Same path, cassette yields 1 (< 3) → real `{ok:false}` → back to `landing`, `last_error` = the script's reason, `captured` unchanged, plan kept for refine. The don't-false-pass case. |
| `plan_mutation_gate.yaml` | Mutation test: breaking the `verify_ok: ok` bind in `verifying.yaml` makes it fail — proves the verify gate is load-bearing, not decorative. |
| `plan_apply_staged_livepath.yaml` | The live-shape regression: STAGED mode + a repo-relative `verify.script`. Fails if `decider: llm` is removed from `verifying` (emit chain stalls) or the raw-path fallback is reverted (script read misses). |
| `incident_mitigate.yaml` | The on-call happy path: alert → triage (recommend mitigate) → auto-route → apply the recorded mitigation (`host.run`) → resolved → postmortem (`host.agent.task`) → landing. Asserts the triage decide fired, the mitigation action recorded, and the postmortem bound. |
| `incident_escalate.yaml` | The sev1 escalation branch: triage (recommend escalate) → page out-of-band (`iface.transport.post`) + inbox mirror (`host.inbox.add`) → resolved → postmortem. Asserts both out-of-band calls fired and `incident_status=escalated`. |
| `incident_monitor_park.yaml` | The low-severity edge: triage (recommend monitor) → `watch` parks the alert back at intake (`status=monitoring`, no action) → a fresh `report_incident` re-arms triage, proving the park is not a dead end. |
| `deploy_succeed.yaml` | The release happy path: target → preflight (go) → ship → verify (healthy) → succeeded → landing. Stubs the two decides; runs REAL `host.run` for preflight/ship/probe. |
| `deploy_rollback.yaml` | The red-after-green branch: preflight go → ship → verify (UNHEALTHY) → rollback → rolled_back read-out. Proves the verify gate is load-bearing — a bad release does not silently stay shipped. |
| `deploy_no_go.yaml` | The blocked-gate branch: preflight (no_go) → `cancel_deploy` parks the deploy → a fresh `start_deploy` re-gates cleanly. |
| `observability_alert.yaml` | The page-now branch: signal → query → triage (alert) → page out-of-band (`iface.transport.post`) + inbox mirror (`host.inbox.add`) → done. |
| `observability_annotate.yaml` | The notable-not-paging branch: triage (annotate) → recorded dashboard note (`host.run`) → done; asserts no page (no transport/inbox calls). |
| `observability_clear_park.yaml` | The nominal edge: triage (clear) → `clear_signal` parks the signal → a fresh `query_signal` re-triages cleanly. |
| `docs_publish.yaml` | The documentation happy path: target → draft (`host.agent.task`, write-mode opt-in) → review → publish (`iface.transport.post`) → published → landing. |
| `docs_revise.yaml` | The revise edge: draft → `revise_doc` parks back at intake (`status=revising`, draft retained) → a fresh `draft_doc` re-arms the writer. |

These are a sample; the full suite passes under `kitsoki test flows stories/dev-story/app.yaml`.

## Manual TUI walkthrough

The same chain `bugfix_to_pr.yaml` exercises is replayable by hand.
Choose the oversight posture before launch with `oversight_gated`,
`oversight_llm_review`, or `oversight_no_gate`. Gated is the human-checkpoint
default (`judge_mode=human`); LLM review sets `judge_mode=llm_then_human`; no
gate keeps LLM judges off and leaves the operation policy drivable.

```
$ kitsoki run stories/dev-story/app.yaml
> tickets                  # landing → ticket_search
> search_tickets open      # → ticket_searching → ticket_search
> pick_ticket TKT-100      # ticket_id / thread populated
> go_bugfix                # → bf.idle
> bf__start                # → bf.reproducing_executing
> bf__proceed              # → bf.reproducing_awaiting_reply
> bf__accept               # → bf.proposing_executing
> bf__proceed              # → bf.proposing_awaiting_reply
> bf__accept               # → bf.implementing_executing
> bf__proceed              # → bf.testing_executing
> bf__proceed              # → bf.testing_awaiting_reply
> bf__accept               # → bf.reviewing_executing
> bf__proceed              # → bf.validating_executing
> bf__proceed              # → bf.validating_awaiting_reply
> bf__accept               # → bf.done_executing
> bf__proceed              # → bf.done_awaiting_reply
> bf__accept               # bf @exit:done → pr.open_pr
> pr__proceed              # → pr.ci_monitoring (CI poll happens in on_enter)
> pr__proceed              # → pr.merge_executing
> pr__proceed              # → pr.merge_awaiting_reply
> pr__accept               # pr @exit:merged → landing (status="merged")
```

With provider bindings configured, the same 20-turn walk-through writes real
diffs, opens a real PR, and merges on the configured forge.

The walkthrough above picks a **bug** and types `go_bugfix`. For a
**feature** ticket (e.g. one filed by the design pipeline), type
`drive` instead of `go_bugfix` after picking — `drive` reads
`ticket_type` and routes into the impl pipeline (`impl.idle`), which
self-provisions a `feature/<ticket>` worktree before the first room
runs. A published design doc can also skip `ticket_search` entirely: from
`design_done`, `implement` drives the freshly-filed feature ticket
straight into impl.

## Demo video: PRD → Design (conversation-driven development)

The dev-story hub's PRD -> Design walk is recorded as a **deterministic, no-LLM
tour video** - the golden example for conversation-driven development (the
[`conversation-driven-development`](../../docs/proposals/conversation-driven-development.md)
epic). The same walk can be retargeted by an importing project instance — see
the [Doc profile](#doc-profile--targeting-an-external-project) section above.
The default fixture uses neutral project-local docs and local feature-ticket
files so it can run without a real ticket provider.

- **Flow fixture (no-LLM):**
  [`flows/prd_to_design_full.yaml`](./flows/prd_to_design_full.yaml) — the
  single-session walk: `main → prd` (discovery + multi-round clarification) →
  `prd_published` (landing) → `continue` → `design` (intake seeded from the PRD)
  → `design_refine` (conversational brief refinement) → `design_draft`
  (publish + mint feature ticket) → `main`. Importing instances can reuse the
  same structure with a different doc tree, template directory, fixed filenames,
  or ticket policy. This one uses the dev-story **defaults** - slug-named docs
  and a local feature ticket on publish.

- **IDE-driven variant (VS Code extension demo):**
  [`flows/prd_to_design_demo.yaml`](./flows/prd_to_design_demo.yaml) — the PRD
  half of the same walk, but deliberately leaving `host.ide.*` and
  `host.artifacts_dir` **unstubbed** so that under `kitsoki web` inside the
  kitsoki VS Code extension (IDE link connected) the brief/PRD are written to
  REAL files and opened in the editor, and a `prd__refine` shows a native
  side-by-side diff with an in-editor Accept/Reject verdict. Under `kitsoki test
  flows` (no IDE) those verbs return `connected:false` / write gitignored
  `.artifacts` files and the refine takes the plain re-draft arc — so it stays a
  valid no-LLM flow. Driven by
  [`tools/vscode-kitsoki/tests/vscode-prd-demo.e2e.spec.ts`](../../tools/vscode-kitsoki/tests/vscode-prd-demo.e2e.spec.ts).

- **Tour manifest + catalog:** the `dev-story-prd-design` catalog entry — 11
  narrated steps that walked every beat of the loop (discovery chat,
  clarification rounds, PRD draft review and publish, design intake handoff,
  design brief refinement, design publish, feature-ticket auto-mint) — was
  **de-listed** from `features/`: the binary `kitsoki tour` renderer it needed
  (shipped; see [`docs/web/tour.md`](../../docs/web/tour.md)) can advance the
  imported PRD state but doesn't remount the chat surface reliably enough for
  Pages CI, so the page shipped as a permanent placeholder. The stub spec
  [`dev-story-prd-design-video.spec.ts`](../../tools/runstatus/tests/playwright/dev-story-prd-design-video.spec.ts)
  is kept for reviving it once that renderer gap closes; the flow fixture's
  *content* is already verified no-LLM under
  `kitsoki test flows stories/dev-story/app.yaml`.

**The canonical conversation-driven-development loop:**

1. **PRD discovery** (`prd.idle → prd.search → prd.clarifying`) — a conversational
   pitch that shapes itself through questions (who's the actor? what's success?)
   into a crisp problem statement, over **multiple** clarification rounds.
2. **PRD publish** (`prd.drafting → accept`) — the draft is authored, reviewed,
   and published to `docs/prd/<slug>.md`.
3. **Design intake** (the `prd_published` handoff → `design`) — the design
   conversation opens *seeded with the published PRD* as prior art, not a blank
   slate (`design_seed_idea` ← `"Author a design from the PRD at <prd_file>"`).
4. **Design brief refinement** (`design → design_search → design_refine → ready`)
   — the brief is scaffolded, gaps are flagged by a refiner, and the operator
   iterates the brief (the same multi-round discipline as PRD clarification)
   before a quality gate clears it.
5. **Design publish + ticket mint** (`design_draft → accept`) — the design
   publishes to `docs/proposals/<slug>.md` and a feature ticket is automatically
   filed at `issues/features/F-<timestamp>-<slug>.md`, linking back to the
   proposal. The ticket can be picked up by the impl pipeline immediately (the
   [`design_to_implementation.yaml`](./flows/design_to_implementation.yaml) bridge).

This single-session closure - from idea to PRD to design to a filed ticket, all
driven by conversation - is the reusable development model that project
wrappers inherit.

See [`docs/skills/kitsoki-ui-demo/SKILL.md`](../../docs/skills/kitsoki-ui-demo/SKILL.md)
for the golden-example pointer and binary-render instructions (slice 2 on).

## Demo: PRD → Design (judge_mode=human)

The [PRD -> Design walk](#prd--design-walk) replayed by hand. With the
standalone defaults or a generated project wrapper that binds providers to
local files:

```
$ kitsoki run stories/dev-story/app.yaml
> prd                       # landing → prd.idle (discovery chat opens)
> I want a CLI for X         # discovery conversation (prd__discuss)
> prd__start                # distil idea → prd.search (prior-art gate)
> prd__confirm              # no overlap → prd.clarifying (questions posed)
> developers; time-to-first-success   # answer (prd__answer); last answer auto-advances
> prd__confirm              # brief → prd.references
> prd__confirm              # references → prd.drafting (PRD authored)
> prd__accept               # publish docs/prd/<slug>.md → prd_published
> continue                  # → design intake, seeded "Author a design from the PRD at …"
> <describe / refine>        # the design pipeline takes over: search → brief → draft → publish
```

`prd_published` also offers `main` to return to the hub without
designing. The deterministic, no-LLM version of this exact walk is
[`flows/prd_to_design.yaml`](./flows/prd_to_design.yaml).

## Agent-split persona table (Phase 8)

The dev-story hub's own agent room makes prose Q&A calls. The
`agent_qa` agent is declared in `app.yaml agents:` and carries
`bash_profile: read-only` (no mutations).

| Persona | Verb | Room |
|---|---|---|
| `agent_qa` | `ask` | `agent_asking` — one-shot prose Q&A answer |

`ask` is the agent-split verb for read-only, prose-output inspection.
It is distinct from `decide` (which requires a JSON schema and emits a
structured verdict) and `task` (which may write files). The agent
persona has `tools: [Read, Grep, Glob]` — codebase inspection without
side effects.

Note: imported sub-stories (`stories/implementation/`,
`stories/code-review/`) were migrated to the new agent verbs in Phase 9.
Flow fixtures that exercise those imports carry `host.agent.decide:` and
`host.agent.ask:` stubs alongside the Phase 8 stubs.

## See also

- [`docs/getting-started.md`](../../docs/getting-started.md) /
  [`docs/stories/dev-story-onboarding.md`](../../docs/stories/dev-story-onboarding.md)
  — the project onboarding pipeline (the `init` rooms) that installs a runnable
  instance + studio MCP + skill/agent toolkit into a target repo.
- [`docs/case-studies/bug-fix.md`](../../docs/case-studies/bug-fix.md)
  — the design.
- [`docs/proposals/notes/dev-story-implementation-contract.md`](../../docs/proposals/notes/dev-story-implementation-contract.md)
  — Wave 1 + Wave 2 contracts.
- [`docs/stories/imports.md`](../../docs/stories/imports.md) — imports authoring
  reference.
- [`stories/bugfix/`](../bugfix/), [`stories/pr-refinement/`](../pr-refinement/)
  — the imported sub-stories.
- [`stories/oregon-trail/`](../oregon-trail/) — three-layer composition
  demo (the pattern this hub mirrors).
- [`docs/architecture/prompt-intercept.md`](../../docs/architecture/prompt-intercept.md)
  — the pre-LLM intercept gate. This hub imports `stories/git-ops/`
  (`imports.gitops`, entry `intercept`; reach the hub from `main` via `git`) to
  surface its command hub for no-LLM interception (`room: gitops.intercept`).
- [`testdata/apps/dev-story/`](../../testdata/apps/dev-story/) — the
  legacy Jira-flavoured stub. Retained for now to keep existing
  loader / metamode / flow tests passing; retired in Wave 3 once no
  test references it.
