# Dynamic Workflows

Dynamic workflows are manifest-driven Kitsoki workflow drafts. They are built as
normal YAML artifacts, validated before launch, and stored under
`.artifacts/dynamic-workflows/<workflow-id>/`.

The runtime wrapper is the generic `punch-list` story. A workflow draft
contains:

- `app/` - a copied story package used for launch;
- `manifest.yaml` - the generated `punch-list/v1` worklist;
- `launch.yaml` - the warp-basis file that seeds `manifest_path`;
- `receipt.json` - the trackable receipt;
- `events.jsonl` - the lifecycle log for generated/validated/launched/exported
  workflow receipts;
- `validation.json` - the deterministic validation report.
- `trace.jsonl` - the launch trace written when the workflow is opened.

The receipt includes a top-level `model_policy` block with the effective
`harness`, `profile`, `model`, `require_gpt55`, and `require_trace_model`
settings. Reviewers can audit the worker policy from `workflow status` without
opening `manifest.yaml`.

Exported runs also write:

- `README.md` - provenance and source-trace summary for the promoted story;
- `flows/generated.yaml` - the starter flow fixture mined from the run trace;
- `flows/generated.cassette.yaml` - the starter host cassette when the trace
  recorded host calls;
- `export-report.json` - the promotion summary, warnings, and TODOs.

## CLI

Create a draft from a free-text goal:

```sh
kitsoki workflow create "implement dynamic workflows" --slug dynamic-workflows
```

Create a draft from an operator-provided decomposition:

```yaml
goal: fix dynamic workflow audit problems
slug: dynamic-workflow-audit
items:
  - id: tui-proof
    title: TUI operation handle proof
    owner_scope: internal/tui
    gate: go test ./internal/tui -run 'Test.*Operation|Test.*Work' -count=1
  - id: corpus-report
    title: Demo corpus runner/report clarity
    owner_scope: internal/testrunner/operation_demo_corpus_test.go
    gate: go test ./internal/testrunner -run TestOperationDemoCorpus -count=1
```

```sh
kitsoki workflow create --brief .context/dynamic-workflow-audit.yaml
```

Structured `items` preserve the supplied IDs, titles, owner scopes, and gates in
the generated `punch-list/v1` manifest instead of replacing them with the
generic `scope` / `implement` / `wire` / `test` phases. `gate` is emitted as the
item's deterministic `gate_command`; use `verify` for richer verifier lists.

Validate, export, or run the draft:

```sh
kitsoki workflow validate dwf_...
kitsoki workflow export dwf_... --target stories/dynamic-workflows
kitsoki workflow run dwf_...
```

`workflow run` shells out to the normal `kitsoki run <app.yaml> --warp <basis>`
entrypoint so the workflow is executed through the existing engine.
`workflow export` writes the promoted story package plus starter deterministic
artifacts into `stories/<slug>/` by default. Exporting into
`internal/basestories/stories/<slug>/` requires `--allow-base-story`.

## Studio MCP

The Studio MCP server exposes the same receipt shape via:

- `workflow.create`
- `workflow.validate`
- `workflow.launch`
- `workflow.status`
- `workflow.export`

`workflow.launch` validates the draft, opens a studio session over
`app/app.yaml`, and returns the tracked receipt with `session_id`,
`session_handle`, `trace_path`, the relative session route (`/s/<id>`), and the
runnable command. The receipt also carries `events_path`, the lifecycle log
written alongside the draft.

`studio.ping` reports both the server build revision and the checkout `HEAD`.
When they differ it returns `stale: true` plus a reload hint. `workflow.create`
and `workflow.launch` refuse stale attached servers so a workflow is not
generated or launched with an older MCP process.

`workflow.create` accepts the same structured shape as `--brief`:

```json
{
  "goal": "fix dynamic workflow audit problems",
  "slug": "dynamic-workflow-audit",
  "items": [
    {
      "id": "tui-proof",
      "title": "TUI operation handle proof",
      "owner_scope": "internal/tui",
      "gate": "go test ./internal/tui -run 'Test.*Operation|Test.*Work' -count=1"
    }
  ]
}
```

## Evidence and limits

Validation is deterministic. The current generator produces a conservative
four-step plan when no structured items or recognized fan-out goal are supplied.
It then validates the copied story package with `app.Load`, the manifest linter,
and launch-readiness flow replay.

Focused deterministic flow gates can name more than one fixture with repeated or
comma-separated `--flows` values:

```sh
kitsoki test flows stories/punch-list/app.yaml \
  --flows stories/punch-list/flows/happy_two_items.yaml,stories/punch-list/flows/skip_current_records.yaml
```

The export path is conservative: it mines the recorded session trace into
starter flow/cassette files and writes an export report listing the review
tasks that still need hand work. MCP, CLI, TUI, and web all call the same
service and return the same receipt fields.
