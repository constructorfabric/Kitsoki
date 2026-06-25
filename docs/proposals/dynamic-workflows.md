# Epic: Dynamic Workflows

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   epic
**Slices:** 4 (0/4 shipped)

## Why

Operators sometimes need a one-off workflow before it is worth hand-authoring a
durable story. The useful loop is: describe the task, get a Kitsoki YAML draft,
validate it before launch, run it with normal trace/session visibility, then
promote a successful run into a reusable story with starter deterministic
artifacts. Kitsoki is already built around that progressive-determinism loop:
ideas become traced runs, repeated decisions become rooms/intents/transitions,
and the story gets more deterministic over time
(`docs/architecture/concept.md:191`).

Kitsoki also already has the safer substrate for dynamic workflows. A story is a
superset of a workflow (`docs/stories/architecture.md:955`), story YAML maps
workflow terms onto rooms, transitions, world, host handlers, sessions, and event
logs (`docs/stories/architecture.md:963`), and Studio MCP exposes deterministic
story authoring tools that wrap the same load/test APIs as the CLI
(`docs/architecture/mcp-studio.md:142`, `internal/mcp/studio/story_tools.go:18`).
The missing product loop is one shared dynamic-workflow service that MCP, CLI,
TUI, and web can all call without creating separate generators or a second
runtime.

## What changes

Kitsoki gains a shared dynamic workflow lifecycle across MCP, CLI, TUI, and web:

1. An operator describes a one-off workflow in natural language.
2. A dependency-injected dynamic workflow service creates a temporary story
   package in YAML, not JavaScript, with explicit rooms, intents, world schema,
   host calls, and validation gates.
3. The service writes a scratch manifest containing workflow id, source task,
   generator surface, model/profile, draft path, validation report hash, allowed
   host capabilities, trace path, session handle, and promotion eligibility.
4. The draft is validated with the existing story loader plus dynamic-specific
   invariants before it can run.
5. The validated draft runs through the existing session engine, trace sink,
   cassettes, and runstatus UI.
6. MCP, CLI, TUI, and web all receive the same receipt: manifest identity,
   validation status, trace path, session handle, and tracking URL when a web
   server is available.
7. After a successful run, export copies the reviewed draft into a project-local
   reusable story plus starter flow fixtures and host cassettes.

V1 ships MCP and CLI first. TUI and web are later adapters over the same service,
not parallel generation paths. Internal base-story promotion is outside the
default path and requires explicit operator approval after project-local export is
proven.

The first implementation should stay conservative: dynamic workflows are ordinary
stories in a scratch namespace with extra provenance, validation, receipt,
promotion, and cleanup metadata. There is no second workflow interpreter.

## Impact

- **Spans:** runtime, story, tracing, TUI/web/CLI/MCP surface.
- **Net surface:** shared dynamic workflow service, new draft-story package
  convention, MCP/CLI create-run-status-export entrypoints, later TUI/web
  adapters, runstatus tracking URL, promotion/export commands.
- **Docs on ship:** `docs/architecture/mcp-studio.md`, `docs/stories/architecture.md`, `docs/stories/state-machine.md`, `docs/tracing/trace-format.md`, `docs/web/README.md`, and a new `docs/stories/dynamic-workflows.md`.
- **Existing proposal set:** keep this epic as the index and amend the four child
  proposals instead of publishing a second design:
  `dynamic-workflow-drafts.md`, `dynamic-workflow-authoring-surfaces.md`,
  `dynamic-workflow-tracking.md`, and `dynamic-workflow-promotion.md`.
- **No-LLM verification:** automated coverage must use mocked generator output,
  flow fixtures, replay harnesses, and host cassettes. Studio MCP and web already
  document replay/flow modes that avoid live LLM calls
  (`docs/architecture/mcp-studio.md:68`, `cmd/kitsoki/web.go:80`).

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | Draft package and validator | runtime | Define the shared service core, scratch story package, manifest, load invariants, provenance, and validation gate for generated workflows. | - | Draft | [`dynamic-workflow-drafts.md`](dynamic-workflow-drafts.md) |
| 2 | Authoring and launch surfaces | story | Add MCP/CLI-first entrypoints, then TUI/web adapters, that create and start dynamic workflows through the same service. | 1 | Draft | [`dynamic-workflow-authoring-surfaces.md`](dynamic-workflow-authoring-surfaces.md) |
| 3 | Tracking URL and execution receipts | tracing | Reuse sessions, traces, runstatus, and Studio MCP handles so every dynamic run has one durable receipt and browser URL when available. | 1, 2 | Draft | [`dynamic-workflow-tracking.md`](dynamic-workflow-tracking.md) |
| 4 | Promotion and cassette export | tracing | Promote a successful scratch workflow into a project-local reusable story and starter deterministic artifacts, with gated base-story export later. | 1, 3 | Draft | [`dynamic-workflow-promotion.md`](dynamic-workflow-promotion.md) |

## Sequencing

```text
#1 shared service + draft package + validator
  -> #2 MCP/CLI create + launch surfaces
  -> #3 tracked execution URL + receipt parity
  -> #4 project-local promotion/export
       -> optional gated base-story promotion
```

Slice 1 must land first because every surface calls the same service, validator,
and scratch-package writer. Slice 2 should expose MCP and CLI before TUI/web so
the shared API hardens without UI-specific coupling. Slice 3 can begin once a
scratch workflow can launch, because Studio MCP already has trace-backed session
handles (`docs/architecture/mcp-studio.md:157`) and runstatus already displays
live traces, world diffs, host invocations, and rendered room views
(`tools/runstatus/README.md:3`). Slice 4 waits until receipts and trace links are
stable enough to export fixtures honestly.

## Shared decisions

1. Dynamic workflows are Kitsoki stories, not a new language. The generated
   artifact is YAML plus scripts/cassettes that load through `app.Load`,
   `story.validate`, `story.test`, `session.new`, and `kitsoki web`.
2. One shared dynamic workflow service owns create, validate, launch, status, and
   export. MCP and CLI call it first; TUI and web call it later through adapters.
3. The interpretive seam is authoring only. Once a draft validates, execution
   uses ordinary transitions, effects, host calls, and deciders. Any LLM decision
   remains a declared agent call with trace records, matching the existing
   `session.drive` boundary (`docs/architecture/mcp-studio.md:159`).
4. Scratch drafts live under `.artifacts/dynamic-workflows/<id>/` by default. The
   manifest records workflow id, source task, generator surface, model/profile,
   draft path, validation report hash, allowed host capabilities, trace path,
   session handle, and promotion eligibility.
5. The receipt shape is common across MCP, CLI, TUI, and web. It returns the
   manifest identity, validation status, trace path, session handle, and a
   runstatus session URL when a web server is available; otherwise it returns the
   trace path and the best available open command.
6. Promotion defaults to project-local `stories/<name>/`. Export to
   `internal/basestories/stories/<name>/` is explicit, gated, and later.
7. Exported flows and cassettes are starters, not claims of complete test
   coverage. Promotion should write an export report with missing gates,
   unresolved live decisions, and hand-authored fixture hardening tasks.

## Cross-cutting Decisions

1. Studio MCP and CLI get first-class `workflow.*` surfaces first. Dev-story,
   TUI, and web may expose conversational or visual entrypoints later, but those
   entrypoints call the same dynamic workflow service rather than generating or
   launching drafts themselves.
2. Validation reports every mutating or external host capability found in the
   generated story. Launch blocks those capabilities until the operator supplies
   an explicit allow-list, and the manifest records the allowed set that was in
   force for the run.
3. Cassette export is conservative. It may generate starter flow and host
   cassette files from trace evidence, but unresolved live model decisions become
   TODOs linked to trace turns rather than fabricated deterministic fixtures.

## Tasks

```text
## 1. Align the proposal set
- [ ] 1.1 Amend `dynamic-workflow-drafts.md` around the shared service contract
      and required manifest fields.
- [ ] 1.2 Amend `dynamic-workflow-authoring-surfaces.md` so MCP/CLI ship first
      and TUI/web are adapters over the same service.
- [ ] 1.3 Amend `dynamic-workflow-tracking.md` so every surface returns one
      receipt shape with workflow id, validation hash, trace/session pointers,
      URL fallback, and promotion eligibility.
- [ ] 1.4 Amend `dynamic-workflow-promotion.md` so project-local story export is
      the default and base-story export is explicit and gated.

## 2. Runtime/service slice
- [ ] 2.1 Design and implement the dependency-injected dynamic workflow service
      for create, validate, launch, status, and export orchestration.
- [ ] 2.2 Define `.artifacts/dynamic-workflows/<id>/` package layout, manifest,
      validation report, and receipt schemas.
- [ ] 2.3 Add dynamic validation invariants on top of ordinary story loading:
      path confinement, declared host capabilities, callable ids suitable for
      fixture export, explicit agent schemas/contracts, and no promotion metadata
      before review.
- [ ] 2.4 Cover the service with deterministic tests using fake generator output,
      replay harnesses, and host cassettes only.

## 3. Surface slice
- [ ] 3.1 Add MCP create/validate/launch/status/export tools or commands over the
      shared service.
- [ ] 3.2 Add CLI `kitsoki workflow create/run/status/export` commands over the
      same service.
- [ ] 3.3 Add the dev-story/TUI lane after MCP/CLI parity exists.
- [ ] 3.4 Add the web action/modal after the receipt and URL behavior are stable.

## 4. Tracking/export slice
- [ ] 4.1 Emit dynamic workflow generated/validated/launched/url/export events
      with hashes and artifact pointers, not full copied YAML blobs.
- [ ] 4.2 Reuse runstatus session URLs when server context exists and return a
      trace/open-command fallback in headless CLI or MCP contexts.
- [ ] 4.3 Export a successful run into `stories/<slug>/` with starter flows,
      starter host cassettes, and an export report listing unresolved decisions.
- [ ] 4.4 Gate `internal/basestories/stories/<slug>/` export behind explicit
      operator approval and clean validation.

## 5. Documentation and cleanup
- [ ] 5.1 Document the shipped operator workflow in
      `docs/stories/dynamic-workflows.md`.
- [ ] 5.2 Update MCP, CLI, tracing, web/runstatus, and story-testing docs with the
      actual command/tool names and receipt schemas.
- [ ] 5.3 Migrate implemented design into docs/ and trim/delete this proposal and
      its child proposals when shipped.
```

## Non-goals

- Running arbitrary JavaScript workflow definitions.
- Keeping a second workflow runtime beside the story engine.
- Auto-promoting generated workflows without operator review.
- Replacing existing flow fixtures or hand-authored story QA.
- Calling a real LLM from automated tests or examples.
