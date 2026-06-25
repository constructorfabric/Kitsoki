# Story: punch-list — run a YAML worklist through Kitsoki stories

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   story
**Epic:**   — standalone; consumed by [`top10-gpt55-dogfood-ingestion.md`](top10-gpt55-dogfood-ingestion.md)

## Why

We need a repeatable way to say "here is a list of work items; run them through
the right Kitsoki stories, like a real operator, and record what happened."
Hard-coding a `top-10` story would solve one backlog and immediately rot. A
generic **punch-list** story lets any YAML list drive the same process: entrypoint
selection, model/profile policy, Studio MCP drive, independent verification,
findings, and a final report.

## What changes

Add a new story, `stories/punch-list/`, whose input is a YAML manifest:

```yaml
version: punch-list/v1
defaults:
  harness: live
  profile: codex-native
  model: gpt-5.5
  trace_root: .artifacts/punch-list/traces
items:
  - id: load-bug
    title: Fix imported bf default expression load failure
    story: stories/kitsoki-dev/app.yaml
    mode: drive
    prompt: "Start from the dogfood hub and reproduce the project-init/bf load failure."
    implementation_story: stories/cherny-loop/app.yaml
    gate_command: "go test ./internal/app ./internal/orchestrator"
    verify:
      - kind: story_validate
        story: stories/kitsoki-dev
      - kind: command
        cmd: "go test ./internal/app ./internal/orchestrator"
  - id: routing-controls
    title: Dogfood contextual routing controls
    story: stories/kitsoki-dev/app.yaml
    mode: drive
    prompt: "Try ambiguous routing phrases and record correction friction."
    fixture_goal: "Add replay fixtures for accepted route decisions."
```

The story loads and lints that manifest, then walks items one at a time:

1. Select the current item.
2. Verify the requested harness/profile/model policy before a live
   implementation attempt.
3. Drive the named story through Studio MCP using either free-form text
   (`mode: drive`) or deterministic intent slots (`mode: submit`).
4. Optionally hand the implementation part to a second story such as
   `cherny-loop`, `deliver`, or `bugfix`, still using the manifest's
   profile/model policy.
5. Run independent verification from the manifest.
6. Record outcome, trace path, model, verifier result, cost/tokens, and
   findings.
7. Continue, skip, retry, or stop at a `needs-human` checkpoint.

## Impact

- **Net-new:** `stories/punch-list/` with ~8 rooms, one manifest schema, a
  linter script, one MCP-driver prompt, and no-LLM flows.
- **Engine/host changes:** ideally none. It composes `host.starlark.run`,
  `host.agent.task`, `host.run`, `host.artifacts_dir`, and Studio MCP driving
  via the existing `kitsoki-mcp-driver` discipline. If a story cannot drive
  Studio MCP from inside the story today, that is surfaced as an MCP/host gap
  rather than hidden.
- **Docs on ship:** `docs/stories/punch-list.md`; update the top-10 dogfood
  proposal to provide a sample manifest instead of a one-off workflow.

## Reuse inventory

| Pipeline step | Mechanism | Reference |
|---|---|---|
| Load/lint YAML manifest | deterministic script / `host.starlark.run` validation | `stories/deliver/scripts/lint_decomposition.py`, `stories/deliver/schemas/decomposition.json` |
| Per-item loop | cyclic dispatcher over `items[index]`, one item per turn | `stories/dogfood-marathon/rooms/processing.yaml` pattern |
| Live Studio MCP drive | `kitsoki-mcp-driver` discipline and trace inspection | `.agents/agents/kitsoki-mcp-driver.md` |
| Model/profile policy | active harness profile supersedes story agent defaults | `.kitsoki.local.yaml.example` `codex-native`; `stories/dogfood-marathon` profile world key |
| Independent verify | run manifest-declared gates; never trust maker self-report | `stories/dogfood-marathon/README.md` honesty posture |
| Findings and report | accumulate per-item records, roll up to artifact | `stories/dogfood-marathon` results/findings/rollup shape |

## Story graph

```
idle ── start manifest_path=... ─▶ load
                                      │
                                      ├─ invalid ─▶ needs-human
                                      ▼
                                  board ◀──────────────────────────────┐
                                    │ next_item                         │
                                    ▼                                   │
                              policy_check                             │
                                    │ ok                                │
                                    ▼                                   │
                                  drive                                 │
                                    │ driven                            │
                                    ▼                                   │
                              implement? ── no ───────────────┐        │
                                    │ yes                      │        │
                                    ▼                          │        │
                              implementation                   │        │
                                    │                          │        │
                                    ▼                          │        │
                                 verify ◀──────────────────────┘        │
                                    │ pass|partial|fail                 │
                                    ▼                                   │
                                record ── continue ────────────────────┘
                                    │ drained
                                    ▼
                                  report ─▶ done
```

`needs-human` is a resumable checkpoint, not a terminal failure. The operator can
`retry`, `skip`, `edit_manifest`, or `file_issue`.

## Manifest schema sketch

```yaml
version: punch-list/v1
defaults:
  harness: live | replay | record
  profile: string
  model: string
  trace_root: string
  require_trace_model: bool
items:
  - id: string
    title: string
    priority: int
    story: string              # app.yaml path to drive first
    state: string              # optional initial/target state
    mode: drive | submit
    prompt: string             # for mode=drive
    intent: string             # for mode=submit
    slots: object
    implementation_story: string
    implementation_prompt: string
    gate_command: string
    profile: string            # overrides defaults.profile
    model: string              # overrides defaults.model
    verify:
      - kind: command | story_validate | story_test | render_tui | render_web
        story: string
        cmd: string
        expect: object
    findings_policy:
      file_mcp_gaps: bool
      file_story_bugs: bool
```

The linter rejects duplicate ids, missing story paths, implementation items
without a deterministic verifier, live implementation items without
`profile: codex-native` and `model: gpt-5.5` when the run policy requires it,
and any verifier that would call a real LLM.

## Per-room detail

### `idle` — choose the manifest

- **Intents:** `start manifest_path=...`, `quit`.
- **View:** manifest path, defaults preview, and a warning that automated tests
  stay no-LLM.

### `load` — parse and lint

- **`on_enter`:** deterministic loader reads YAML, validates `punch-list/v1`,
  normalizes defaults onto each item, and binds `punch_items`.
- **Routes:** valid -> `board`; invalid -> `needs-human` with `last_error`.

### `board` — choose next work item

- Shows every item with `pending | running | passed | partial | failed | skipped`
  status, trace link, and findings count.
- `next_item` selects the next unprocessed item by priority/order.
- Operator actions: `pick id=...`, `skip id=...`, `report`.

### `policy_check` — enforce harness/profile/model

- For live implementation attempts, verifies the item's resolved policy is
  allowed. For the GPT-5.5 dogfood list, that means `profile: codex-native` and
  `model: gpt-5.5`.
- If Studio MCP cannot prove the actual model selection after the first turn,
  record an MCP finding and park at `needs-human`.

### `drive` — exercise the target story like an operator

- Delegates the Studio MCP turn sequence to the `kitsoki-mcp-driver` discipline:
  create/attach session, drive human phrasing, inspect outcome, inspect trace,
  render if relevant, and close abandoned sessions.
- Stores `drive_result = {story, handle, trace_path, outcome, state, model}`.

### `implementation` — optional maker pass

- Runs only when the item has `implementation_story`.
- Typical values: `stories/cherny-loop/app.yaml` for bounded runtime work,
  `stories/deliver/app.yaml` for proposal implementation, or `stories/bugfix`
  for filed bugs.
- The room must pass through the same profile/model policy and record trace proof.

### `verify` — independent gate

- Executes the manifest's `verify[]` list. A command or story test decides the
  result, not the maker's prose.
- Binds `verify_result = {status, checks, stdout_refs}`.

### `record` — append the result

- Appends `{id, status, trace_path, model, verifier, findings, cost, tokens}` to
  `punch_results`.
- Emits `continue` back to `board`.

### `report` / `done` — rollup

- Produces a markdown summary under `.artifacts/punch-list/` and optionally a
  media/report artifact if the existing report producers are available.

## Flow fixtures

- `lint_rejects_bad_model_policy` — live implementation item with
  `profile: claude-native` is rejected under a GPT-5.5-only policy.
- `happy_two_items_stubbed` — two manifest items run through drive -> verify ->
  record -> report with every host call stubbed.
- `needs_human_on_mcp_gap` — driver cannot prove model/trace, parks with a
  finding and offers retry/skip/file_issue.
- `skip_and_continue` — operator skips one item and the board continues.
- `verify_failure_records_partial` — verifier fails and the result records
  `partial` or `failed` without claiming success.

## Tasks

```
## 1. Manifest and board
- [ ] 1.1 Define `schemas/punch-list.json` and sample manifests, including the
          current top-10 GPT-5.5 list.
- [ ] 1.2 Implement deterministic load/lint script and no-LLM table tests.
- [ ] 1.3 Build `idle`, `load`, and `board` rooms with typed views.

## 2. Drive and verify loop
- [ ] 2.1 Add `policy_check` with model/profile enforcement.
- [ ] 2.2 Add `drive` and `implementation` rooms that delegate to the Studio MCP
          driver path and bind trace/model evidence.
- [ ] 2.3 Add manifest-declared `verify[]` execution and result recording.

## 3. Findings and report
- [ ] 3.1 Add findings board/actions (`retry`, `skip`, `file_issue`,
          `make_proposal`, `route_to_bugfix`).
- [ ] 3.2 Add markdown report artifact and rollup screen.
- [ ] 3.3 Flow fixtures pass with no real LLM.

## 4. Dogfood and retire proposal
- [ ] 4.1 Run the top-10 manifest through `stories/punch-list/` via Studio MCP.
- [ ] 4.2 Harden any general story/MCP friction found.
- [ ] 4.3 Migrate to `docs/stories/punch-list.md` and delete this proposal.
```

## Open questions

1. **Can a story directly drive Studio MCP sessions today?** The
   `kitsoki-mcp-driver` agent can use MCP tools, but an in-story
   `host.agent.task` may not have those tools unless launched in the right
   environment. *Lean: v1 treats `drive` as a delegated driver task and files an
   MCP/host gap if the story cannot invoke it cleanly.*
2. **Manifest compatibility with `fleet`/`deliver`.** `fleet` already consumes a
   decomposition-like YAML, but punch-list items need story entrypoints and
   verification policy. *Lean: separate `punch-list/v1`; provide a converter from
   decomposition manifests later.*
3. **Report format.** Markdown first or slidey report? *Lean: markdown first for
   low friction, optional media report once the core loop is stable.*

## Non-goals

- A top-10-specific story.
- Bypassing each target story's own workflow.
- Trusting live model output without deterministic verification.
- Running real LLMs from flow tests.
