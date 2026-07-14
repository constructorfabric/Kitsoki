---
name: kitsoki-bug-filing
description: File Kitsoki bug reports through the project-supported filing paths, preserving screenshots, HARs, rrweb replays, TUI transcripts, trace evidence, GitHub artifact links, and Kitsoki metadata. Use when the user asks to file/report a Kitsoki bug, turn captured evidence into a GitHub issue, attach or host bug artifacts through the GitHub agent, use the web/TUI Report Bug surfaces, or convert a finding from dogfood/debugging into a durable issue without bypassing Kitsoki's formatting and workflow logic.
---

# Kitsoki Bug Filing

## Overview

File bugs through Kitsoki's own entrypoints so the report carries the right
frontmatter, metadata block, evidence links, redaction, and GitHub upload
behavior. Do not use raw `gh issue create` as the normal filing path; use a
Kitsoki story or command that applies the repo's bug-filing conventions.

## Decision Tree

1. **Live web UI bug with screenshot/HAR/rrweb/console evidence:** use the web
   story surface and Meta -> Report bug with `kitsoki web --ticket-repo
   <owner/repo>`. This routes `runstatus.bug.report` through
   `host.GitHubFileBug(... UploadArtifacts: true)`, saves a local copy under
   `.artifacts/bug-reports/`, uploads evidence as GitHub release assets, and
   links those assets from the issue.
2. **Live TUI bug with transcript/context evidence:** run the relevant Kitsoki
   story in the TUI with `--ticket-repo <owner/repo>`, then use `/bug
   <description>`. This writes scrubbed transcript/context artifacts and files
   through the same GitHub bug orchestration with uploaded evidence.
3. **Existing rrweb/HAR evidence that needs a hosted reviewer artifact:** push
   the evidence into the deployed GitHub agent's evidence store, then trigger the
   agent with a signed `kitsoki gh-agent replay` webhook. The hosted deck comment
   must be authored by the GitHub App bot, not by the operator's `gh` identity.
4. **Text-only issue with no evidence:** `kitsoki bug create --github
   <owner/repo>` is acceptable, but note that it captures no artifacts. Do not
   use it when the user asked for artifacts.
5. **Product-journey / dogfood run-bundle findings with attached evidence:**
   do NOT hand-file these (no manual `issue_create`, no raw `gh`). The
   product-journey story files them itself: `file_findings
   ticket_repo=<owner/repo>` on `stories/product-journey-qa` (or the headless
   fallback `python3 tools/product-journey/run.py --file-findings --run-dir
   <run-dir> --ticket-repo <owner/repo>`). Both drive `kitsoki bug
   file-findings` → `host.GitHubFileFindings` → `host.GitHubFileBug(...
   UploadArtifacts: true)`: one issue per credible `issue` finding, evidence
   uploaded as release assets, expected/actual/repro body assembled from the
   finding + driver journal, and the issue URL recorded back into
   `findings.json` so re-runs are idempotent. Use `mode=dry-run` / `--dry-run`
   to preview.
6. **Other dogfood or debugging findings (no run bundle):** file via the live
   story/studio path when available. In the studio MCP, `issue.create` should be
   the zero-friction path: with one current live session, a call with just
   `title`/`body` infers the handle and includes redacted trace + inspect
   evidence by default. When the bug happened in another TUI/session run, find
   the JSONL with `kitsoki trace` / `kitsoki trace status`, then call
   `issue.create` with `trace_path` or with `trace_ref` plus optional
   `trace_app` / `trace_ticket`; the tool resolves the trace, reconstructs the
   latest world from `world.update`, and writes redacted trace/world sidecars.
   Pass `include_trace:false` / `include_inspect:false` only when deliberately
   filing a text-only issue. Anything with captured web or TUI artifact evidence
   should still go through the richer web/TUI paths above instead of shelling
   out to `gh`.

## Filing Workflows

### Web Report Bug

Use this when the browser view itself is the evidence.

```bash
go run ./cmd/kitsoki web stories/<story>/app.yaml --ticket-repo <owner/repo>
```

Then use the web Meta launcher -> Report bug, review the captured evidence, and
submit. The command path captures browser evidence, scrubs it, writes the local
artifact copy, uploads evidence through the GitHub artifact release path, and
creates the issue with links.

Prefer this over hand-built markdown when the user says "file this with
artifacts", "include the screenshot/HAR/replay", or "open the issue in GitHub
linking to the evidence".

### TUI `/bug`

Use this when the bug is visible while operating a Kitsoki story in the TUI.

```bash
go run ./cmd/kitsoki run stories/<story>/app.yaml --ticket-repo <owner/repo>
```

In the TUI, run:

```text
/bug <one-line summary, then any useful detail>
```

With `--ticket-repo`, `/bug` stores scrubbed TUI evidence under
`.artifacts/bug-reports/<id>/`, uploads it as GitHub release assets, and links it
from the GitHub issue. Without `--ticket-repo`, it writes a local
`.artifacts/issues/bugs/<id>.md` plus sibling `<id>.artifacts/` in the source
checkout. Managed capsule workspaces never own the durable ticket.

### Hosted GitHub Agent Deck

Use this after a GitHub issue exists and the useful evidence is an rrweb/HAR
bundle that should be reviewable through the GitHub agent's hosted deck. The
normal path is the deployed agent webhook trigger:

```bash
# 1. Put the evidence where the deployed agent reads it. The destination id is:
#    $(kitsoki-internal formula) bugdeck.DeckID(<owner/repo>, <issue>)
#    For bsacrobatix/Kitsoki#68, for example: bsacrobatix-Kitsoki-68.
scp -r <local-evidence-dir>/<deck-id> \
  <agent-host>:<agent-evidence-dir>/

# 2. Re-deliver a signed issue webhook so the deployed agent renders, hosts, and
#    comments as the GitHub App installation.
go run ./cmd/kitsoki gh-agent replay \
  --repo <owner/repo> \
  --issue <number> \
  --action opened \
  --url <agent-public-url>/gh-agent/webhook \
  --secret "$KITSOKI_GH_WEBHOOK_SECRET"
```

When filing from `kitsoki web`, prefer starting it with `--agent-evidence-dir`
pointed at the agent's evidence store when that path is mounted locally. If the
agent is off-box, copy the deposited `<deck-id>/har.json` and/or
`<deck-id>/rrweb.json` directory to the agent before replaying the webhook.

`kitsoki gh-agent deck` is a diagnostic/off-box renderer for local proof or
repair. Do not use `kitsoki gh-agent deck --comment` as the normal filing path:
that posts through the caller's local `gh` authentication, so the issue comment
will be authored by the user instead of the GitHub App.

### Product-Journey Findings (`kitsoki bug file-findings`)

Use this when a tools/product-journey run bundle has recorded `issue` findings
that must become GitHub issues without an outer agent hand-assembling bodies.

```bash
# Preferred: the story files its own findings.
#   file_findings ticket_repo=<owner/repo>            (mode=dry-run to preview)
# Headless fallback (drives the same Go orchestration):
python3 tools/product-journey/run.py --file-findings \
  --run-dir .artifacts/product-journey/<run-id> \
  --ticket-repo <owner/repo> [--dry-run]
# Direct CLI (what the runner shells to):
go run ./cmd/kitsoki bug file-findings \
  --run-dir .artifacts/product-journey/<run-id> --repo <owner/repo> [--dry-run]
```

Per credible finding (kind `issue`, origin not `seeded`, no recorded issue
yet) this uploads locally-resolvable evidence as release assets, files the
issue with `## Artifacts` + the kitsoki metadata block, and writes
`item.github_issue` (URL/number/repo/filed_at) plus a `findings.filing` block
back into `findings.json`. Re-runs skip already-filed findings, and the
runner's `--review-run` / `--validate-run` gates (`findings-filed`) then
require every credible finding to be filed.

### Text-Only Fallback

Only when there are no artifacts to preserve:

```bash
go run ./cmd/kitsoki bug create \
  --target kitsoki \
  --github <owner/repo> \
  --title "<short title>" \
  --body "<expected / actual / impact>" \
  --severity med \
  --component <component>
```

This still uses Kitsoki's formatting and metadata path, but it is text-only.
Never substitute it for the web/TUI artifact paths when evidence exists.

### Studio MCP `issue.create`

Use this when a Kitsoki MCP-driven session has surfaced a bug and there is no
web/TUI Report Bug surface attached. Prefer the current-session default:

```json
{"title":"mcp: <short failure>", "body":"<expected / actual / impact>"}
```

When exactly one current live driving session is open, `issue.create` infers the
handle, writes scrubbed session evidence under `.artifacts/mcp-issues/<slug>/`,
embeds a compact redacted trace + inspect snapshot in the issue body, and adds
runtime metadata. Supply `handle` only when multiple sessions are open. Do not
paste raw `session.trace` output into the body; let `issue.create` apply the
shared bug redactor. Use `include_visual_recordings` or explicit `assets` for
extra MCP-produced screenshots/recordings.

For a bug that happened in a TUI/session run outside the current MCP process,
first locate the trace with `kitsoki trace --turns ...` or `kitsoki trace status
...`, then file with a trace source:

```json
{"title":"tui: <short failure>", "body":"<expected / actual / impact>", "trace_ref":"<filename substring>", "trace_app":"kitsoki-dev"}
```

Use `trace_path` when you already have the exact JSONL path. Add
`trace_ticket` when debugging found the relevant ticket id and you want the
server to pick the newest matching trace. Trace-backed reports include redacted
`trace.redacted.jsonl` and `world.redacted.json` sidecars by default.

## Report Shape

Keep the title short and actionable, prefixed by the surface when useful
(`web:`, `tui:`, `mcp:`, `story:`).

For evidence-backed reports, do not require the reporter to hand-author
expected/actual/repro fields. A tiny note plus artifacts is valuable. The filing
path should preserve the raw note, attach the evidence, and add a deterministic
`## Evidence-derived triage` section when possible:

- Capture summary from HAR, rrweb, console/error state, and redacted trace.
- Evidence-derived likely repro steps, labeled as inferred.
- Evidence-derived actual behavior from failures/errors/transitions.
- Expected behavior only when the reporter explicitly supplied it; otherwise an
  explicit "not deterministically captured" note.

For text-only reports with no artifacts, the body should include:

- What the operator expected.
- What actually happened.
- A minimal reproduction path or the story/session entrypoint.
- Why it matters.
- Any available links to traces, decks, or local `.artifacts` paths.

Let Kitsoki write the metadata/frontmatter where possible. Do not hand-copy the
fenced `kitsoki` metadata block, issue labels, release asset URLs, or local
artifact layout unless you are repairing a failed filing and have inspected the
generated body.

Every supported filing path must preserve runtime version metadata. GitHub
issues carry it in the fenced `kitsoki` metadata block; local markdown carries
it in frontmatter. Keep these fields when repairing or migrating reports:
`engine_version`, `engine_revision`, `engine_revision_short`, `engine_dirty`,
`engine_checksum_sha256`, `story_app_id`, `story_app_version`, `story_entry`,
`story_checksum_sha256`, and `public_stories_json`.

## Validation

Before claiming the bug is filed, verify the artifact path that matters:

- Web/TUI GitHub path: confirm the command returned a GitHub issue URL and the
  issue body has an `## Artifacts` section with GitHub release asset links or a
  hosted deck comment, plus the fenced `kitsoki` block with engine and story
  runtime metadata.
- Studio MCP path: confirm `issue.create` returned a GitHub issue URL or
  `local_path`; for a handle-backed report the body should include redacted
  `## Context` / `## Trace` sections, link `trace.redacted.jsonl` and
  `world.redacted.json` sidecars under `.artifacts/mcp-issues/`, and keep the
  fenced `kitsoki` metadata block.
- Local path: confirm `.artifacts/issues/bugs/<id>.md` exists in the source
  checkout and its sibling `<id>.artifacts/` contains the expected evidence;
  the markdown frontmatter
  should include the same engine and story runtime metadata.
- Hosted deck path: confirm `kitsoki gh-agent deck` printed the public deck URL
  and, when `--comment` was used, the issue has the comment.

Automated tests must not call real GitHub or real LLMs. Use fake `cliExec`,
host cassettes, flow fixtures, or targeted unit tests around the Kitsoki
orchestration (`internal/host`, `internal/runstatus/server`, `internal/tui`) for
regressions.
