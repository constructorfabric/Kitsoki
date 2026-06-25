# Tracing: Dynamic Workflow Tracking URLs

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   tracing
**Epic:**   [`dynamic-workflows.md`](dynamic-workflows.md)

## Why

The operator should get back a URL or receipt they can trust. Kitsoki already has trace-backed Studio MCP session handles, `session.trace`, and runstatus web views. The runstatus SPA displays the live trace, state timeline, agent calls, world diffs, host invocations, and rendered room view (`tools/runstatus/README.md:3`). Dynamic workflows should use that existing visibility rather than inventing a separate tracker.

## What Changes

When a validated draft launches, Kitsoki returns a dynamic workflow run receipt:

- workflow id;
- manifest path and hash;
- draft package path;
- session id and handle when applicable;
- trace path;
- web runstatus URL when a web server is available;
- validation report hash;
- allowed host capabilities used for launch;
- promotion/export eligibility state.

The receipt is written to `.artifacts/dynamic-workflows/<id>/receipt.json`.
Trace events carry the receipt path and hash, not a second copy of the whole
payload. MCP/CLI/TUI/web all surface the same receipt.

## Impact

- **Trace events:** dynamic workflow generated, validated, launched, URL assigned, exported.
- **Runstatus:** route/open affordance for dynamic workflow receipts.
- **MCP:** `session.new` and `studio.work` can expose dynamic workflow metadata.
- **Docs on ship:** `docs/tracing/trace-format.md`, `docs/architecture/mcp-studio.md`, `docs/web/README.md`.

## Event Model

| Event | Required fields | Notes |
|---|---|---|
| `dynamic.workflow.generated` | `workflow_id`, `draft_dir`, `files`, `prompt_hash`, `manifest_hash` | Emitted after generation. |
| `dynamic.workflow.validated` | `workflow_id`, `ok`, `errors`, `warnings`, `validator_version`, `validation_report_hash` | Deterministic. |
| `dynamic.workflow.launch_blocked` | `workflow_id`, `blocked_capabilities`, `validation_report_hash` | Emitted when required host capabilities are not explicitly allowed. |
| `dynamic.workflow.launched` | `workflow_id`, `session_id`, `trace_path`, `story_path`, `allowed_host_capabilities`, `receipt_hash` | Links draft to session. |
| `dynamic.workflow.url_assigned` | `workflow_id`, `url`, `server_id` | Emitted only when a browser URL exists. |
| `dynamic.workflow.exported` | `workflow_id`, `target_dir`, `artifacts`, `receipt_hash` | Promotion/export slice owns details. |

## URL Behavior

- In `kitsoki web`, launch returns a live URL because the server hosts the session in-process.
- In Studio MCP, `render.web` already serves live handles through the runstatus handler when possible (`docs/architecture/mcp-studio.md:250`); dynamic workflow launch should reuse that path for web-capable servers.
- In CLI-only mode, the receipt returns the trace path and the suggested command to view it.

Example receipt fallback:

```text
Workflow dwf_123 launched.
Trace: .artifacts/dynamic-workflows/dwf_123/run.jsonl
Open:  kitsoki web --trace .artifacts/dynamic-workflows/dwf_123/run.jsonl
```

If `kitsoki web --trace` is not the current command shape, implementation should use the existing runstatus export/static viewer path instead of inventing a stale command.

## Decision Recording

The tracking layer must not summarize away the run. It records pointers and hashes, while the session trace remains authoritative. Receipts are indexes into evidence, not evidence substitutes.

## Tasks

```text
## 1. Receipt
- [ ] 1.1 Define `receipt.json` schema and writer.
- [ ] 1.2 Attach workflow metadata to session handles and trace context.
- [ ] 1.3 Add deterministic tests for receipt creation and missing-web fallback.

## 2. Trace
- [ ] 2.1 Add dynamic workflow event constants and tests.
- [ ] 2.2 Add trace-format documentation.
- [ ] 2.3 Verify replay ignores receipt-only metadata unless explicitly consumed.

## 3. Runstatus
- [ ] 3.1 Surface dynamic workflow id and promotion state in the session header.
- [ ] 3.2 Add an Open URL action where server context exists.
- [ ] 3.3 Add Playwright coverage using flow/host cassettes only.

## 4. MCP/CLI/TUI
- [ ] 4.1 Return the same receipt shape from every surface.
- [ ] 4.2 Update docs and trim/delete this proposal.
```

## Verification

Use a fake generated draft and no-LLM host cassettes. Verify that trace events are present, receipt paths exist, runstatus can open the session, and CLI/MCP return identical core fields.

## Decisions

1. The receipt is both an artifact and a trace pointer. The canonical JSON lives
   under `.artifacts/dynamic-workflows/<id>/receipt.json`; the trace records its
   path and hash so evidence remains discoverable without duplicating large
   payloads in every event.
2. `studio.work` lists dynamic workflows as distinct work items only when
   operator action is required, such as blocked launch capabilities, validation
   failure, or export review. Otherwise the run appears through ordinary session
   and trace surfaces.

## Non-goals

- Building a separate workflow dashboard.
- Storing full generated YAML inside every trace event.
- Making URL availability mandatory in headless CLI mode.
