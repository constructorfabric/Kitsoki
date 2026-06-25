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

## Evidence and limits

Validation is deterministic. The current generator produces a conservative
four-step plan and a `punch-list/v1` manifest, then validates the copied story
package with `app.Load` plus the manifest linter.

The export path is conservative: it mines the recorded session trace into
starter flow/cassette files and writes an export report listing the review
tasks that still need hand work. MCP, CLI, TUI, and web all call the same
service and return the same receipt fields.
