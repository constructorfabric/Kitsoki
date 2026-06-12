# The web UI operator surface

`kitsoki web` drives the same orchestrator as the [terminal UI](README.md)
over HTTP — a multi-story browser plus chat-style session surfaces beside a
live trace and state diagram, served from `internal/runstatus/` with a Vue
front-end under `tools/runstatus/`. This page documents the operator-facing
*meta* surfaces unique to the browser; the shared session/reload semantics
live in [`../web/README.md`](../web/README.md).

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

What is captured, anonymized, and (on Submit) written:

- **Screenshot** — client-side via `html2canvas` over the app root (a PNG of
  the rendered DOM; no browser permission prompt).
- **rrweb replay** — client-side DOM recording with `maskAllInputs` enabled.
  Input masking is the **privacy boundary** for committed artifacts: typed
  values never enter the recording, so the replay is safe to commit.
- **Console + error state** — recent console entries and any captured error
  state, serialized alongside the replay.
- **HAR** — server-side. The runstatus server mediates every RPC/SSE call, so
  a bounded ring buffer keeps the last N request/response pairs and serializes
  them as a HAR 1.2 archive. This sees request/response bodies that page-JS
  reconstruction cannot.
- **Anonymize** — deterministic, server-side, before anything is written:
  strips `Authorization` / `Cookie` / `Set-Cookie` headers and known
  session-token query params, redacts absolute paths under `$HOME`, and
  redacts configured secret-shaped values.
- **File** — on Submit, writes a flat `issues/bugs/<id>.md` (same format and
  frontmatter as `kitsoki bug create`, with the operator description plus
  `## Error state` and `## Console (recent)` sections) plus a sibling
  `issues/bugs/<id>.artifacts/` holding `screenshot.png`, `har.json`,
  `rrweb.json`, and `console.json`, linked from the ticket's `## Artifacts`
  section. The ticket and its artifacts are committed to the repo, same as
  hand-filed seeds.

The on-disk format, the artifacts-folder convention, and the proof that a
sibling `.artifacts/` folder does not disturb the `issues/bugs/*.md` ticket
reader are documented once in [`issues/README.md`](../../issues/README.md) and
[`../stories/bugs.md`](../stories/bugs.md).

The item is **hidden in snapshot / artifact (read-only) mode**, exactly like
the rest of the Meta button — there is no running session or live transport to
capture from.
