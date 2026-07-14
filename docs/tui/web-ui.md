# The web UI operator surface

`kitsoki web` drives the same orchestrator as the [terminal UI](README.md)
over HTTP — a multi-story browser plus chat-style session surfaces beside a
live trace and state diagram, served from `internal/runstatus/` with a Vue
front-end under `tools/runstatus/`. This page documents the operator-facing
*meta* surfaces unique to the browser; the shared session/reload semantics
live in [`../web/README.md`](../web/README.md).

> The same SPA also ships **inside VS Code** — chat in the sidebar, trace in a
> bottom panel — as a third head on the orchestrator. The embed relays this app's
> JSON-RPC/SSE over `postMessage` to a spawned `kitsoki web`; see the
> [VS Code extension](vscode-extension.md) for the transport seam, theming, and the
> full-editor demo pipeline.

The browser is also where an operator can **point at a frame** and ask the
read-only agent about the element under the click — the
[spatial capture](spatial-capture.md) surface (a terminal operator reaches the
same picker through the [spatial handoff](spatial-handoff.md) window).

## Meta menu → Report bug

The Meta dropdown (bottom-right) carries a **Report bug** item that captures a
complete, evidence-backed bug and opens it in a **review modal** before
anything is written — no shell, no blind filing. It is a deterministic
capture action, distinct from the agentic `story.bug` / `kitsoki.bug`
conversation modes (see [`../stories/bugs.md`](../stories/bugs.md) §1.1).

Clicking **Report bug** captures evidence and opens the review modal showing:

- an **rrweb session replay** of the recorded interaction (scrubbable via
  `rrweb-player`);
- the **scrubbed HAR** — a readable request/response summary with the raw
  HAR 1.2 archive available in an expandable panel;
- the captured **console log and error state**;
- an optional **description** field for the operator.

The operator reviews this evidence and clicks **Submit** to file, or
**Cancel** to discard without writing anything. This is the privacy and
quality gate: nothing reaches disk until a human has seen exactly what will
be committed. It resolves the proposal's Open Question §3 (operator
review-before-file).

What is captured, anonymized, summarized, and (on Submit) written:

- **rrweb replay** — client-side DOM recording with `maskAllInputs` enabled.
  Input masking is the **privacy boundary** for committed artifacts: typed
  values never enter the recording, so the replay is safe to commit.
- **Console + error state** — recent console entries and any captured error
  state, serialized alongside the replay.
- **HAR** — server-side. The runstatus server mediates every RPC/SSE call, so
  a bounded ring buffer keeps the last N request/response pairs and serializes
  them as a HAR 1.2 archive. This sees request/response bodies that page-JS
  reconstruction cannot.
- **Trace evidence** — when the report is attached to a live session, a
  depersonalized `trace.redacted.jsonl` sidecar preserves states, turns,
  intents, and transitions without copying user free text.
- **Evidence-derived triage** — server-side and deterministic. The filed
  markdown gets a generated section with capture summary, likely repro steps,
  observed actual behavior from network/console/error/trace evidence, and an
  explicit note when expected behavior is not deterministically captured.
  Reporter prose can stay short.
- **Anonymize** — deterministic, server-side, before anything is written:
  strips `Authorization` / `Cookie` / `Set-Cookie` headers and known
  session-token query params, redacts absolute paths under `$HOME`, and
  redacts configured secret-shaped values.
- **File** — on Submit, writes a flat `.artifacts/issues/bugs/<id>.md` (same format and
  frontmatter as `kitsoki bug create`, with the operator description plus
  `## Evidence-derived triage`, `## Error state`, and `## Console (recent)`
  sections) plus a sibling
  `.artifacts/issues/bugs/<id>.artifacts/` holding `har.json`, `rrweb.json`,
  `console.json`, `trace.redacted.jsonl`, and any optional screenshot when
  present, linked from the ticket's `## Artifacts` section. The ticket and its
  artifacts are durable local review artifacts, not committed work-in-progress
  tickets.

The on-disk format and the artifacts-folder convention are documented once in
[`issues/README.md`](../../issues/README.md) and
[`../stories/bugs.md`](../stories/bugs.md). Start `kitsoki web --ticket-repo
<owner/repo>` when the report should become a GitHub Issue for autonomous
agent intake.

The item is **hidden in snapshot / artifact (read-only) mode**, exactly like
the rest of the Meta button — there is no running session or live transport to
capture from.
