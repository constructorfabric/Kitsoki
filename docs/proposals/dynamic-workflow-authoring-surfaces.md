# Story: Dynamic Workflow Authoring Surfaces

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   story
**Epic:**   [`dynamic-workflows.md`](dynamic-workflows.md)

## Why

The operator should not have to know whether the current entrypoint is MCP, CLI, TUI, or web. The same request, "make a workflow for this task and run it with gates," should produce the same draft package, validation result, and launch receipt. Studio MCP already exposes authoring and driving primitives, while `kitsoki web` hosts live sessions that the browser can observe and drive (`cmd/kitsoki/web.go:65`). The missing piece is one shared product lane that all surfaces call.

## What Changes

Add dynamic workflow authoring surfaces over one shared service:

- **Studio MCP:** first-class `workflow.create`, `workflow.validate`,
  `workflow.launch`, `workflow.status`, and `workflow.export` tools backed by
  the service.
- **CLI:** `kitsoki workflow create "..."`, `kitsoki workflow validate <id>`,
  `kitsoki workflow run <id>`, `kitsoki workflow status <id>`, and
  `kitsoki workflow export <id>`.
- **TUI:** a dev-story room reached naturally from landing, with review and
  launch choices, calling the same service.
- **Web:** a runstatus action/modal that calls the same service and navigates to
  the returned session URL.

V1 ships MCP and CLI first. TUI and web are adapters added after the service
contract, receipt shape, and launch posture are stable.

## Impact

- **Story files:** `stories/dev-story/rooms/`, prompts, schemas, deterministic flows.
- **MCP/CLI/web/TUI:** thin launch adapters and status rendering.
- **Hosts:** draft generation host function, validator call, launch call.
- **Docs on ship:** `stories/dev-story/README.md`, `docs/architecture/mcp-studio.md`, `docs/web/README.md`.

## Story Shape

```text
landing
  -> dynamic_workflow_intake
  -> dynamic_workflow_draft_review
  -> dynamic_workflow_validation_failed | dynamic_workflow_ready
  -> dynamic_workflow_running
  -> dynamic_workflow_done
```

The room should be conversational at intake but explicit at gates:

- `revise` updates the generated draft prompt or selected files.
- `validate` runs deterministic validation again.
- `launch` opens a session only when validation passed.
- `export` is hidden until a run has a trace and validation evidence.

## Deterministic vs Interpretive

Interpretive:

- turning the operator's natural-language request into draft YAML;
- proposing gate names and validation commands;
- summarizing warnings for the operator.

Deterministic:

- writing the package;
- loading and validating the package;
- launching the session;
- rendering status and run URLs;
- exporting flow/cassette starters.

## MCP / CLI / Web Parity

All surfaces should return a common receipt:

```json
{
  "workflow_id": "dwf_...",
  "draft_dir": ".artifacts/dynamic-workflows/dwf_.../story",
  "manifest_path": ".artifacts/dynamic-workflows/dwf_.../dynamic.workflow.json",
  "validation_report_hash": "sha256:...",
  "validation": { "ok": true, "warnings": [] },
  "allowed_host_capabilities": ["host.git.status"],
  "promotion_eligibility": "eligible",
  "session": { "id": "...", "trace": "...", "url": "http://127.0.0.1:.../runs/..." },
  "next_actions": ["open", "revise", "export"]
}
```

If no web server is available, `url` is empty and the receipt includes the command needed to open the trace in runstatus.

## Tasks

```text
## 1. Shared Host/API
- [ ] 1.1 Add a dependency-injected create/validate/launch service.
- [ ] 1.2 Expose the service to Studio MCP without duplicating CLI code.
- [ ] 1.3 Add no-LLM unit tests around the service using fake generator output.

## 2. MCP and CLI Surfaces
- [ ] 2.1 Add first-class Studio MCP workflow tools.
- [ ] 2.2 Add CLI `kitsoki workflow create/validate/run/status/export`
      commands.
- [ ] 2.3 Ensure create stops at review by default; only `--run` launches.

## 3. TUI/Web Adapters
- [ ] 3.1 Add dev-story rooms for intake, review, validate, launch, and done.
- [ ] 3.2 Add schemas/prompts for draft generation and warning summaries.
- [ ] 3.3 Add flow fixtures with mocked agent output and host handlers.
- [ ] 3.4 Add a TUI command/menu path from dev-story landing.
- [ ] 3.5 Add a web action that calls the same backend and navigates to the run.

## 4. Docs
- [ ] 4.1 Document the operator flow and no-LLM test posture.
- [ ] 4.2 Move shipped design to docs and trim/delete this proposal.
```

## Verification

- `story.validate` for dev-story after room additions.
- `story.test` or `go run ./cmd/kitsoki test flows stories/dev-story/app.yaml --flows <dynamic fixtures>`.
- MCP smoke using replay/flow cassettes only.
- Web Playwright test with `kitsoki web --flow`, no live LLM.

## Decisions

1. Studio MCP exposes first-class `workflow.*` tools. A dev-story room may offer
   a conversational operator path later, but it calls the same service and does
   not replace the MCP API.
2. CLI creation stops at review by default. Launch requires either an explicit
   `kitsoki workflow run <id>` command or a creation-time `--run` flag with the
   needed host-capability allow-list.

## Non-goals

- Making generated workflows bypass operator review.
- Replacing dev-story's design pipeline.
- Adding a visual workflow editor in v1.
